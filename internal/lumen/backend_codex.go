package lumen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// codexTranslator 把 codex app-server 的 notification / server request 轉成 normalized Event。
// 它是純函式：threadID / turnID 只用來過濾，不會被修改。
type codexTranslator struct {
	threadID string
	turnID   string
}

// codexRequest 是 server request 的翻譯結果：event 給 UI，reject 與 denyPermissions 需要 backend 回覆 codex。
type codexRequest struct {
	event           Event
	reject          string
	denyPermissions bool
}

func (t codexTranslator) notification(message rpcMessage) []Event {
	switch message.Method {
	case "turn/started":
		var params struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.ThreadID == t.threadID {
			return []Event{EvTurnStarted{SessionID: params.ThreadID, TurnID: params.Turn.ID}}
		}
	case "item/started":
		return t.itemStarted(message.Params)
	case "item/agentMessage/delta":
		var params struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			Delta    string `json:"delta"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.ThreadID == t.threadID {
			return []Event{EvAssistantDelta{TurnID: params.TurnID, Text: params.Delta}}
		}
	case "item/commandExecution/outputDelta":
		var params struct {
			ThreadID string `json:"threadId"`
			ItemID   string `json:"itemId"`
			Delta    string `json:"delta"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.ThreadID == t.threadID {
			return []Event{EvToolOutput{ToolID: params.ItemID, Text: params.Delta}}
		}
	case "item/completed":
		return t.itemCompleted(message.Params)
	case "turn/plan/updated":
		var params struct {
			ThreadID string `json:"threadId"`
			Plan     []struct {
				Step   string `json:"step"`
				Status string `json:"status"`
			} `json:"plan"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.ThreadID == t.threadID {
			items := make([]PlanItem, 0, len(params.Plan))
			for _, item := range params.Plan {
				items = append(items, PlanItem{Text: item.Step, Status: item.Status})
			}
			return []Event{EvPlanUpdated{Items: items}}
		}
	case "thread/name/updated":
		var params struct {
			ThreadID string `json:"threadId"`
			Name     string `json:"name"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.ThreadID == t.threadID {
			return []Event{EvSessionTitle{SessionID: params.ThreadID, Title: params.Name}}
		}
	case "thread/tokenUsage/updated":
		var params struct {
			ThreadID   string `json:"threadId"`
			TokenUsage struct {
				Total struct {
					TotalTokens              int64 `json:"totalTokens"`
					InputTokens              int64 `json:"inputTokens"`
					OutputTokens             int64 `json:"outputTokens"`
					CachedInputTokens        int64 `json:"cachedInputTokens"`
					CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
					CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
				} `json:"total"`
				ModelContextWindow *int64 `json:"modelContextWindow"`
			} `json:"tokenUsage"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.ThreadID == t.threadID {
			total := params.TokenUsage.Total
			cacheRead := total.CacheReadInputTokens
			if cacheRead == 0 {
				cacheRead = total.CachedInputTokens
			}
			return []Event{EvUsage{
				Used: total.TotalTokens,
				Size: params.TokenUsage.ModelContextWindow,
				Breakdown: &UsageBreakdown{
					Input:         total.InputTokens,
					Output:        total.OutputTokens,
					CacheRead:     cacheRead,
					CacheCreation: total.CacheCreationInputTokens,
				},
			}}
		}
	case "turn/completed":
		var params struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"turn"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.ThreadID == t.threadID {
			event := EvTurnCompleted{TurnID: params.Turn.ID, Status: codexTurnStatus(params.Turn.Status), Label: params.Turn.Status}
			if params.Turn.Error != nil && params.Turn.Error.Message != "" {
				event.Err = errors.New(params.Turn.Error.Message)
			}
			return []Event{event}
		}
	case "error":
		// app-server 的 ErrorNotification 是 {error: {message}, willRetry}；舊形狀 {message} 仍接受。
		var params struct {
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
			WillRetry bool `json:"willRetry"`
		}
		if json.Unmarshal(message.Params, &params) == nil {
			text := valueOr(params.Error.Message, params.Message)
			switch {
			case text == "":
			case params.WillRetry:
				return []Event{EvNotice{Level: NoticeWarn, Text: text + " · retrying"}}
			default:
				return []Event{EvNotice{Level: NoticeError, Text: text}}
			}
		}
	case "warning", "configWarning", "guardianWarning":
		var params struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.Message != "" {
			return []Event{EvNotice{Level: NoticeWarn, Text: compactWarning(params.Message)}}
		}
	}
	return nil
}

func codexTurnStatus(status string) TurnStatus {
	switch status {
	case "interrupted":
		return TurnInterrupted
	case "failed":
		return TurnFailed
	default:
		return TurnCompleted
	}
}

func (t codexTranslator) itemStarted(raw json.RawMessage) []Event {
	var params struct {
		ThreadID string         `json:"threadId"`
		TurnID   string         `json:"turnId"`
		Item     map[string]any `json:"item"`
	}
	if json.Unmarshal(raw, &params) != nil || params.ThreadID != t.threadID {
		return nil
	}
	typeName, _ := params.Item["type"].(string)
	itemID, _ := params.Item["id"].(string)
	switch typeName {
	case "commandExecution":
		command, _ := params.Item["command"].(string)
		return []Event{EvToolStarted{ToolID: itemID, Kind: ToolCommand, Title: "$ " + command, Command: command}}
	case "fileChange":
		return []Event{EvToolStarted{ToolID: itemID, Kind: ToolEdit, Title: "✎ file change"}}
	case "mcpToolCall":
		server, _ := params.Item["server"].(string)
		tool, _ := params.Item["tool"].(string)
		return []Event{EvToolStarted{ToolID: itemID, Kind: ToolMCP, Title: fmt.Sprintf("◇ %s/%s", server, tool)}}
	case "reasoning":
		return []Event{EvReasoningStarted{TurnID: params.TurnID}}
	}
	return nil
}

func (t codexTranslator) itemCompleted(raw json.RawMessage) []Event {
	var params struct {
		ThreadID string         `json:"threadId"`
		TurnID   string         `json:"turnId"`
		Item     map[string]any `json:"item"`
	}
	if json.Unmarshal(raw, &params) != nil || params.ThreadID != t.threadID {
		return nil
	}
	typeName, _ := params.Item["type"].(string)
	itemID, _ := params.Item["id"].(string)
	status, _ := params.Item["status"].(string)
	switch typeName {
	case "commandExecution":
		label := toolCompletionStatus(status, params.Item["exitCode"])
		return []Event{EvToolCompleted{ToolID: itemID, Status: codexToolStatus(label), Label: label}}
	case "fileChange":
		return []Event{EvToolCompleted{ToolID: itemID, Status: codexToolStatus(status), Label: status}}
	case "reasoning":
		return []Event{EvReasoningCompleted{TurnID: params.TurnID}}
	}
	return nil
}

func codexToolStatus(label string) ToolStatus {
	lower := strings.ToLower(label)
	switch {
	case strings.Contains(lower, "fail") || strings.Contains(lower, "exit"):
		return ToolFailed
	case strings.Contains(lower, "cancel") || strings.Contains(lower, "declin") || strings.Contains(lower, "interrupt"):
		return ToolCancelled
	default:
		return ToolOK
	}
}

func (t codexTranslator) request(request serverRequest) codexRequest {
	reqID := string(request.id)
	switch request.method {
	case "item/commandExecution/requestApproval":
		var params struct {
			ThreadID           string   `json:"threadId"`
			TurnID             string   `json:"turnId"`
			ItemID             string   `json:"itemId"`
			Command            string   `json:"command"`
			CWD                string   `json:"cwd"`
			Reason             string   `json:"reason"`
			AvailableDecisions []string `json:"availableDecisions"`
		}
		if err := json.Unmarshal(request.params, &params); err != nil {
			return codexRequest{reject: "invalid approval parameters"}
		}
		if err := t.validate(params.ThreadID, params.TurnID); err != nil {
			return codexRequest{reject: err.Error()}
		}
		return codexRequest{event: EvApprovalRequest{
			ReqID:   reqID,
			TurnID:  params.TurnID,
			Title:   params.Command,
			CWD:     params.CWD,
			Reason:  params.Reason,
			Options: codexApprovalOptions(params.AvailableDecisions),
		}}
	case "item/fileChange/requestApproval":
		var params struct {
			ThreadID           string   `json:"threadId"`
			TurnID             string   `json:"turnId"`
			Reason             string   `json:"reason"`
			AvailableDecisions []string `json:"availableDecisions"`
		}
		if err := json.Unmarshal(request.params, &params); err != nil {
			return codexRequest{reject: "invalid file-change approval parameters"}
		}
		if err := t.validate(params.ThreadID, params.TurnID); err != nil {
			return codexRequest{reject: err.Error()}
		}
		return codexRequest{event: EvApprovalRequest{
			ReqID:   reqID,
			TurnID:  params.TurnID,
			Title:   "apply file changes",
			Reason:  params.Reason,
			Options: codexApprovalOptions(params.AvailableDecisions),
		}}
	case "item/permissions/requestApproval":
		var params struct {
			ThreadID string          `json:"threadId"`
			TurnID   string          `json:"turnId"`
			Request  json.RawMessage `json:"permissions"`
		}
		if err := json.Unmarshal(request.params, &params); err != nil {
			return codexRequest{reject: "invalid permission parameters"}
		}
		if err := t.validate(params.ThreadID, params.TurnID); err != nil {
			return codexRequest{reject: err.Error()}
		}
		return codexRequest{denyPermissions: true}
	case "item/tool/requestUserInput":
		var params struct {
			ThreadID  string              `json:"threadId"`
			TurnID    string              `json:"turnId"`
			Questions []userInputQuestion `json:"questions"`
		}
		if err := json.Unmarshal(request.params, &params); err != nil {
			return codexRequest{reject: "invalid user-input parameters"}
		}
		if err := t.validate(params.ThreadID, params.TurnID); err != nil {
			return codexRequest{reject: err.Error()}
		}
		for _, question := range params.Questions {
			if question.ID == "" || question.Question == "" {
				return codexRequest{reject: "user-input question is missing id or question"}
			}
		}
		return codexRequest{event: EvInputRequest{ReqID: reqID, TurnID: params.TurnID, Questions: params.Questions}}
	default:
		return codexRequest{reject: "unsupported server request: " + request.method}
	}
}

func (t codexTranslator) validate(threadID, turnID string) error {
	if threadID == "" {
		return errors.New("server request missing thread id")
	}
	if threadID != t.threadID {
		return errors.New("server request thread mismatch")
	}
	if turnID != "" && t.turnID != "" && turnID != t.turnID {
		return errors.New("server request turn mismatch")
	}
	return nil
}

// codexApprovalOptions 把 availableDecisions 轉成 Options；server 沒給時視為四種都可用。
// 未知的 decision 保留為 OptionOther，讓 UI 維持 fail closed。
func codexApprovalOptions(available []string) []ApprovalOption {
	if len(available) == 0 {
		available = []string{"accept", "acceptForSession", "decline", "cancel"}
	}
	options := make([]ApprovalOption, 0, len(available))
	for _, decision := range available {
		kind := OptionOther
		switch decision {
		case "accept":
			kind = OptionAllowOnce
		case "acceptForSession":
			kind = OptionAllowAlways
		case "decline":
			kind = OptionRejectOnce
		case "cancel":
			kind = OptionCancel
		}
		options = append(options, ApprovalOption{ID: decision, Label: decision, Kind: kind})
	}
	return options
}

func compactWarning(message string) string {
	const prefix = "Under-development features enabled: "
	if !strings.HasPrefix(message, prefix) {
		return message
	}
	features := strings.TrimSpace(strings.SplitN(strings.TrimPrefix(message, prefix), ".", 2)[0])
	if features == "" {
		return "Codex warning: unstable features active"
	}
	return "Codex warning: unstable features active (" + features + ")"
}

// codexBackend 包裝 appServer，把 codex 專屬協定隔離在這個檔案與 app.go。
type codexBackend struct {
	client *appServer

	mu         sync.Mutex
	translator codexTranslator

	events    chan Event
	done      chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

var (
	_ Backend     = (*codexBackend)(nil)
	_ CodexExtras = (*codexBackend)(nil)
	_ ModelPicker = (*codexBackend)(nil)
)

func newCodexBackend(client *appServer, sessionID string) *codexBackend {
	return &codexBackend{
		client:     client,
		translator: codexTranslator{threadID: sessionID},
		events:     make(chan Event, 256),
		done:       make(chan struct{}),
	}
}

func (b *codexBackend) Name() string { return "codex" }

func (b *codexBackend) Caps() Capabilities {
	return Capabilities{
		SessionList:    true,
		SessionResume:  true,
		ReplayOnResume: true,
		ForkAtTurn:     true,
		Goal:           true,
		Review:         true,
		Compact:        true,
		ModelList:      true,
		UserInput:      true,
		TokenBreakdown: true,
		NativeAdmin:    true,
		Rename:         true,
		Archive:        true,
		Delete:         true,
		ImagePrompt:    true,
		SlashCatalog:   "static",
	}
}

// Events 在第一次呼叫時才啟動 pump，避免只用來送 RPC 的 backend 多出一個 goroutine。
func (b *codexBackend) Events() <-chan Event {
	b.startOnce.Do(func() { go b.pump() })
	return b.events
}

func (b *codexBackend) Disconnected() (bool, error) {
	select {
	case <-b.client.closed:
		return true, b.client.failureError()
	default:
	}
	if err := b.client.failureError(); err != nil {
		return true, err
	}
	return false, nil
}

func (b *codexBackend) Open(ctx context.Context) error {
	return b.client.Initialize()
}

func (b *codexBackend) NewSession(cwd string, rt runtimeOverrides) (SessionInfo, error) {
	return b.adopt(b.client.StartThread(cwd, rt))
}

func (b *codexBackend) ResumeSession(id, cwd string, rt runtimeOverrides) (SessionInfo, error) {
	return b.adopt(b.client.ResumeThread(id, rt))
}

func (b *codexBackend) ListSessions(cwd string, limit int) ([]SessionInfo, error) {
	return b.client.ListThreads(cwd, limit)
}

// Prompt 在 StartTurn 回傳後立刻記下 turn id，使 turn/started 到達前的 server request 也能做 turn 檢查。
func (b *codexBackend) Prompt(sessionID, text string, images []promptImage, rt runtimeOverrides) (string, error) {
	turnID, err := b.client.StartTurn(sessionID, text, images, rt)
	if err == nil && turnID != "" {
		b.mu.Lock()
		b.translator.turnID = turnID
		b.mu.Unlock()
	}
	return turnID, err
}

func (b *codexBackend) Cancel(sessionID, turnID string) error {
	return b.client.Interrupt(sessionID, turnID)
}

func (b *codexBackend) ResolveApproval(reqID, optionID string) error {
	return b.client.respond(json.RawMessage(reqID), map[string]string{"decision": optionID})
}

func (b *codexBackend) RejectRequest(reqID, message string) error {
	return b.client.respondError(json.RawMessage(reqID), -32001, message)
}

func (b *codexBackend) AnswerInput(reqID string, answers map[string][]string) error {
	converted := make(map[string]userInputAnswer, len(answers))
	for id, values := range answers {
		converted[id] = userInputAnswer{Answers: values}
	}
	return b.client.respond(json.RawMessage(reqID), map[string]any{"answers": converted})
}

func (b *codexBackend) Close() {
	b.closeOnce.Do(func() {
		close(b.done)
		b.client.Close()
	})
}

func (b *codexBackend) Fork(id string, rt runtimeOverrides) (SessionInfo, error) {
	return b.adopt(b.client.ForkThread(id, rt))
}

func (b *codexBackend) ForkAt(id, lastTurnID string, rt runtimeOverrides) (SessionInfo, error) {
	return b.adopt(b.client.ForkThreadAt(id, lastTurnID, rt))
}

func (b *codexBackend) Compact(id string) error { return b.client.CompactThread(id) }

func (b *codexBackend) Review(id string, target map[string]any) (string, error) {
	return b.client.StartReview(id, target)
}

func (b *codexBackend) Goal(id string) (map[string]any, error) { return b.client.GetThreadGoal(id) }

func (b *codexBackend) SetGoal(id, objective string) (map[string]any, error) {
	return b.client.SetThreadGoal(id, objective)
}

func (b *codexBackend) ClearGoal(id string) error { return b.client.ClearThreadGoal(id) }

func (b *codexBackend) ListModels() ([]codexModel, error) { return b.client.ListModels() }

// ModelOptions 把 model/list 轉成 backend-neutral 的清單；ID 是 --model / turn override 使用的值。
func (b *codexBackend) ModelOptions() ([]modelOption, error) {
	models, err := b.client.ListModels()
	if err != nil {
		return nil, err
	}
	options := make([]modelOption, 0, len(models))
	for _, model := range models {
		name := valueOr(model.DisplayName, valueOr(model.Model, model.ID))
		if model.Hidden {
			name += " · hidden"
		}
		options = append(options, modelOption{ID: valueOr(model.Model, model.ID), Name: name})
	}
	return options, nil
}

func (b *codexBackend) ListSkills(cwd string) ([]codexSkillEntry, error) {
	return b.client.ListSkills(cwd)
}

func (b *codexBackend) ListHooks(cwd string) ([]codexHookEntry, error) {
	return b.client.ListHooks(cwd)
}

func (b *codexBackend) ListMCPServers(sessionID string) ([]codexMCPServer, error) {
	return b.client.ListMCPServers(sessionID)
}

func (b *codexBackend) ListApps() ([]codexApp, error) { return b.client.ListApps() }

func (b *codexBackend) ListPlugins(cwd string) ([]codexPluginMarketplace, error) {
	return b.client.ListPlugins(cwd)
}

func (b *codexBackend) Archive(id string) error { return b.client.ArchiveThread(id) }

func (b *codexBackend) Delete(id string) error { return b.client.DeleteThread(id) }

func (b *codexBackend) Rename(id, name string) error { return b.client.SetThreadName(id, name) }

func (b *codexBackend) Call(method string, params any, target any) error {
	return b.client.Call(method, params, target)
}

// adopt 在 session 切換成功時更新過濾用的 thread id，失敗則維持原 session。
func (b *codexBackend) adopt(thread threadSummary, err error) (SessionInfo, error) {
	if err == nil {
		b.setState(thread.ID, "")
	}
	return thread, err
}

func (b *codexBackend) setState(threadID, turnID string) {
	b.mu.Lock()
	b.translator = codexTranslator{threadID: threadID, turnID: turnID}
	b.mu.Unlock()
}

func (b *codexBackend) translate(message rpcMessage) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	events := b.translator.notification(message)
	for _, event := range events {
		switch event := event.(type) {
		case EvTurnStarted:
			b.translator.turnID = event.TurnID
		case EvTurnCompleted:
			b.translator.turnID = ""
		}
	}
	return events
}

// handleRequest 翻譯 server request，並替 UI 回覆 codex 需要 backend 自己回的情況。
func (b *codexBackend) handleRequest(request serverRequest) []Event {
	b.mu.Lock()
	result := b.translator.request(request)
	b.mu.Unlock()
	switch {
	case result.reject != "":
		if b.client != nil {
			_ = b.client.respondError(request.id, -32602, "lumen: "+result.reject)
		}
		return []Event{EvRequestRejected{ReqID: string(request.id), Message: result.reject}}
	case result.denyPermissions:
		if err := b.client.respond(request.id, map[string]any{
			"permissions": map[string]any{},
			"scope":       "turn",
		}); err != nil {
			return []Event{EvNotice{Level: NoticeError, Text: err.Error()}}
		}
		return []Event{EvNotice{Level: NoticeWarn, Text: "permission request denied · no additional access granted"}}
	default:
		return []Event{result.event}
	}
}

func (b *codexBackend) send(event Event) bool {
	select {
	case b.events <- event:
		return true
	case <-b.done:
		return false
	}
}

func (b *codexBackend) pump() {
	defer close(b.events)
	client := b.client
	for {
		select {
		case <-b.done:
			return
		case request := <-client.requests:
			if !b.sendAll(b.handleRequest(request)) {
				return
			}
		case message := <-client.notifications:
			if !b.sendAll(b.translate(message)) {
				return
			}
		case err := <-client.errors:
			if err != nil {
				b.send(EvConnectionLost{Err: err})
				return
			}
		case <-client.closed:
			if !b.drain() {
				return
			}
			b.send(EvConnectionLost{Err: client.failureError()})
			return
		}
	}
}

// drain 在連線結束時把已排隊的訊息送完，避免最後一段 output 被吃掉。
func (b *codexBackend) drain() bool {
	for {
		select {
		case request := <-b.client.requests:
			if !b.sendAll(b.handleRequest(request)) {
				return false
			}
		case message := <-b.client.notifications:
			if !b.sendAll(b.translate(message)) {
				return false
			}
		default:
			return true
		}
	}
}

func (b *codexBackend) sendAll(events []Event) bool {
	for _, event := range events {
		if !b.send(event) {
			return false
		}
	}
	return true
}
