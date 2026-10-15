package lumen

// Fake ACP agent（T2.6）：測試 binary 以 helper process 方式重新執行自己，
// 從 stdin/stdout 講 ACP（line-delimited JSON-RPC）。不依賴網路或真實 credentials。
//
// 行為由環境變數設定 agent 層級行為，由 session/prompt 的文字選擇 prompt 層級 scenario。
// 所有收到的訊息與啟動資訊（argv、cwd、env key）寫到 $LUMEN_FAKEACP_LOG（JSON lines）。

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	fakeACPFlag = "LUMEN_FAKEACP"
	fakeACPLog  = "LUMEN_FAKEACP_LOG"
)

func TestFakeACPAgentProcess(t *testing.T) {
	if os.Getenv(fakeACPFlag) != "1" {
		return
	}
	runFakeACPAgent(os.Stdin, os.Stdout, os.Stderr)
	os.Exit(0)
}

type fakeACPAgent struct {
	out  io.Writer
	logf *os.File

	outMu sync.Mutex
	logMu sync.Mutex

	respMu    sync.Mutex
	waiters   map[string]chan json.RawMessage
	nextReqID int

	cancel chan struct{}

	modelMu      sync.Mutex
	currentModel string // 空字串代表 envOr("FAKEACP_MODEL", "opus")
	effort       string // 空字串代表 FAKEACP_EFFORT（未設定時 configOptions 沒有 effort）
	noEffort     bool   // true = configOptions 不再有 effort（模擬換到不支援 effort 的 model）
	mode         string // 空字串代表 envOr("FAKEACP_MODE", "default")
	style        string // 空字串代表 "quiet"；只有 FAKEACP_EXTRA=1 才有 style option
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func runFakeACPAgent(in io.Reader, out io.Writer, stderr io.Writer) {
	agent := &fakeACPAgent{out: out, waiters: map[string]chan json.RawMessage{}, cancel: make(chan struct{}, 16)}
	if path := os.Getenv(fakeACPLog); path != "" {
		agent.logf, _ = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	}
	cwd, _ := os.Getwd()
	var envKeys []string
	for _, entry := range os.Environ() {
		envKeys = append(envKeys, strings.SplitN(entry, "=", 2)[0])
	}
	agent.logLine(map[string]any{"start": map[string]any{"argv": os.Args, "cwd": cwd, "env": envKeys}})

	if lines := os.Getenv("FAKEACP_STDERR_LINES"); lines != "" {
		var count int
		fmt.Sscanf(lines, "%d", &count)
		for i := 0; i < count; i++ {
			fmt.Fprintf(stderr, "stderr line %d %s\n", i, strings.Repeat("x", 600))
		}
	}

	reader := bufio.NewReaderSize(in, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			agent.dispatch(line)
		}
		if err != nil {
			return
		}
	}
}

type fakeACPMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func (a *fakeACPAgent) logLine(value any) {
	if a.logf == nil {
		return
	}
	encoded, _ := json.Marshal(value)
	a.logMu.Lock()
	defer a.logMu.Unlock()
	a.logf.Write(append(encoded, '\n'))
}

func (a *fakeACPAgent) write(value map[string]any) {
	value["jsonrpc"] = "2.0"
	encoded, _ := json.Marshal(value)
	a.outMu.Lock()
	defer a.outMu.Unlock()
	a.out.Write(append(encoded, '\n'))
}

func (a *fakeACPAgent) reply(id json.RawMessage, result any) {
	a.write(map[string]any{"id": id, "result": result})
}

func (a *fakeACPAgent) replyError(id json.RawMessage, code int, message string) {
	a.write(map[string]any{"id": id, "error": map[string]any{"code": code, "message": message}})
}

func (a *fakeACPAgent) update(session string, update map[string]any) {
	a.write(map[string]any{"method": "session/update", "params": map[string]any{"sessionId": session, "update": update}})
}

func (a *fakeACPAgent) message(session, text string) {
	a.update(session, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}})
}

// request 對 client 送 request（id 從 0 開始，與真實 adapter 一致）並等待 response。
func (a *fakeACPAgent) request(method string, params any) fakeACPMessage {
	a.respMu.Lock()
	id := a.nextReqID
	a.nextReqID++
	wait := make(chan json.RawMessage, 1)
	a.waiters[fmt.Sprint(id)] = wait
	a.respMu.Unlock()
	a.write(map[string]any{"id": id, "method": method, "params": params})
	select {
	case raw := <-wait:
		var response fakeACPMessage
		json.Unmarshal(raw, &response)
		return response
	case <-time.After(10 * time.Second):
		return fakeACPMessage{}
	}
}

func (a *fakeACPAgent) dispatch(line []byte) {
	a.logLine(map[string]any{"recv": json.RawMessage(strings.TrimSpace(string(line)))})
	var message fakeACPMessage
	if json.Unmarshal(line, &message) != nil {
		return
	}
	if message.Method == "" && len(message.ID) > 0 {
		a.respMu.Lock()
		wait := a.waiters[string(message.ID)]
		delete(a.waiters, string(message.ID))
		a.respMu.Unlock()
		if wait != nil {
			wait <- append(json.RawMessage(nil), line...)
		}
		return
	}
	switch message.Method {
	case "initialize":
		a.initialize(message)
	case "session/new":
		if os.Getenv("FAKEACP_NEW_ERROR") == "auth" {
			a.replyError(message.ID, -32000, "Authentication required")
			return
		}
		a.reply(message.ID, a.sessionResult("sess-new"))
	case "session/load":
		a.load(message)
	case "session/resume":
		a.reply(message.ID, a.sessionResult(""))
	case "session/set_config_option":
		a.setConfigOption(message)
	case "session/set_model":
		a.setSessionModel(message)
	case "session/list":
		a.list(message)
	case "session/cancel":
		select {
		case a.cancel <- struct{}{}:
		default:
		}
	case "session/prompt":
		go a.prompt(message)
	default:
		if len(message.ID) > 0 {
			a.replyError(message.ID, -32601, "method not found")
		}
	}
}

func (a *fakeACPAgent) hasCap(name string) bool {
	for _, item := range strings.Split(envOr("FAKEACP_CAPS", "load,list"), ",") {
		if item == name {
			return true
		}
	}
	return false
}

func (a *fakeACPAgent) initialize(message fakeACPMessage) {
	version := 1
	if value := os.Getenv("FAKEACP_PROTOCOL"); value != "" {
		fmt.Sscanf(value, "%d", &version)
	}
	sessionCaps := map[string]any{}
	if a.hasCap("list") {
		sessionCaps["list"] = map[string]any{}
	}
	if a.hasCap("resume") {
		sessionCaps["resume"] = map[string]any{}
	}
	authMethods := []any{}
	if os.Getenv("FAKEACP_AUTH") == "1" {
		authMethods = append(authMethods, map[string]any{"id": "login", "name": "Login"})
	}
	a.reply(message.ID, map[string]any{
		"protocolVersion": version,
		"agentCapabilities": map[string]any{
			"loadSession":         a.hasCap("load"),
			"sessionCapabilities": sessionCaps,
			"promptCapabilities":  map[string]any{"image": false},
		},
		"authMethods": authMethods,
	})
}

func (a *fakeACPAgent) sessionResult(id string) map[string]any {
	result := map[string]any{
		"modes": map[string]any{
			"currentModeId":  a.currentMode(),
			"availableModes": []any{map[string]any{"id": "default"}, map[string]any{"id": "plan"}},
		},
	}
	if id != "" {
		result["sessionId"] = id
	}
	a.addModels(result)
	return result
}

// fakeACPModels 是 fake adapter 提供的 model 清單（value / name / description）。
var fakeACPModels = [][3]string{
	{"default", "Default (recommended)", "Opus 5.5"},
	{"opus", "Opus 5.5", "complex work"},
	{"sonnet", "Sonnet 5.5", "simpler tasks"},
}

func (a *fakeACPAgent) model() string {
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	return valueOr(a.currentModel, envOr("FAKEACP_MODEL", "opus"))
}

func (a *fakeACPAgent) setModel(id string) {
	a.modelMu.Lock()
	a.currentModel = id
	a.modelMu.Unlock()
}

func (a *fakeACPAgent) currentEffort() string {
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	if a.noEffort {
		return ""
	}
	return valueOr(a.effort, os.Getenv("FAKEACP_EFFORT"))
}

func (a *fakeACPAgent) setEffort(value string) {
	a.modelMu.Lock()
	a.effort = value
	a.modelMu.Unlock()
}

func (a *fakeACPAgent) currentMode() string {
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	return valueOr(a.mode, envOr("FAKEACP_MODE", "default"))
}

func (a *fakeACPAgent) currentStyle() string {
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	return valueOr(a.style, "quiet")
}

// setConfigValue 依 configId 寫入 fake agent 的狀態；未知的 configId 回傳 false。
func (a *fakeACPAgent) setConfigValue(id, value string) bool {
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	switch id {
	case "model":
		a.currentModel = value
	case "effort":
		a.effort = value
	case "mode":
		a.mode = value
	case "style":
		a.style = value
	default:
		return false
	}
	return true
}

func fakeACPModelOption(current string, grouped bool) map[string]any {
	var flat []any
	for _, item := range fakeACPModels {
		flat = append(flat, map[string]any{"value": item[0], "name": item[1], "description": item[2]})
	}
	options := flat
	if grouped {
		options = []any{
			map[string]any{"group": "recommended", "name": "Recommended", "options": flat[:2]},
			map[string]any{"group": "other", "name": "Other", "options": flat[2:]},
		}
	}
	return map[string]any{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": current, "options": options}
}

// configOptions 回傳 fake adapter 目前的 configOptions（mode 加上 model）。
func (a *fakeACPAgent) configOptions() []any {
	options := []any{
		map[string]any{"id": "mode", "name": "Mode", "description": "Session permission mode", "category": "mode", "type": "select", "currentValue": a.currentMode(),
			"options": []any{map[string]any{"value": "default", "name": "Manual"}, map[string]any{"value": "plan", "name": "Plan"}, map[string]any{"value": "acceptEdits", "name": "Accept edits"}}},
		fakeACPModelOption(a.model(), os.Getenv("FAKEACP_MODELS") == "grouped"),
	}
	if effort := a.currentEffort(); effort != "" {
		options = append(options, map[string]any{"id": "effort", "name": "Effort", "category": "thought_level", "type": "select", "currentValue": effort,
			"options": []any{map[string]any{"value": "low", "name": "Low"}, map[string]any{"value": "high", "name": "High"}, map[string]any{"value": "xhigh", "name": "Extra high"}}})
	}
	if os.Getenv("FAKEACP_EXTRA") == "1" {
		// style 是 Lumen 完全不認得的 option（沒有 category）；brief 是 boolean，必須被忽略。
		// blast / shout 的 name 相同，用來測 display name 的 ambiguity。
		options = append(options,
			map[string]any{"id": "style", "name": "Style", "description": "Reply style", "type": "select", "currentValue": a.currentStyle(),
				"options": []any{
					map[string]any{"value": "quiet", "name": "Quiet", "description": "short answers"},
					map[string]any{"value": "loud", "name": "Loud"},
					map[string]any{"value": "blast", "name": "Twin"},
					map[string]any{"value": "shout", "name": "Twin"},
				}},
			map[string]any{"id": "brief", "name": "Brief", "type": "boolean", "currentValue": true})
	}
	return options
}

// addModels 依 FAKEACP_MODELS（config | grouped | legacy；空 = 沒有 model 資訊）加上 model 欄位。
func (a *fakeACPAgent) addModels(result map[string]any) {
	switch os.Getenv("FAKEACP_MODELS") {
	case "config", "grouped":
		result["configOptions"] = a.configOptions()
	case "legacy":
		var available []any
		for _, item := range fakeACPModels {
			available = append(available, map[string]any{"modelId": item[0], "name": item[1], "description": item[2]})
		}
		result["models"] = map[string]any{"currentModelId": a.model(), "availableModels": available}
	}
}

func (a *fakeACPAgent) setConfigOption(message fakeACPMessage) {
	var params struct {
		ConfigID string `json:"configId"`
		Value    string `json:"value"`
	}
	json.Unmarshal(message.Params, &params)
	if os.Getenv("FAKEACP_SET_ERROR") != "" {
		a.replyError(message.ID, -32602, "invalid value")
		return
	}
	if !a.setConfigValue(params.ConfigID, params.Value) {
		a.replyError(message.ID, -32602, "unknown config option")
		return
	}
	a.reply(message.ID, map[string]any{"configOptions": a.configOptions()})
}

func (a *fakeACPAgent) setSessionModel(message fakeACPMessage) {
	var params struct {
		ModelID string `json:"modelId"`
	}
	json.Unmarshal(message.Params, &params)
	if os.Getenv("FAKEACP_SET_ERROR") != "" {
		a.replyError(message.ID, -32602, "invalid model")
		return
	}
	a.setModel(params.ModelID)
	a.reply(message.ID, map[string]any{})
}

func (a *fakeACPAgent) load(message fakeACPMessage) {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	json.Unmarshal(message.Params, &params)
	chunk := func(kind, text string) {
		a.update(params.SessionID, map[string]any{"sessionUpdate": kind, "content": map[string]any{"type": "text", "text": text}})
	}
	chunk("user_message_chunk", "hello agent")
	chunk("agent_message_chunk", "hi ")
	chunk("agent_message_chunk", "there")
	a.update(params.SessionID, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "r1", "title": "ls", "kind": "execute", "status": "pending"})
	a.update(params.SessionID, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "r1", "status": "completed",
		"content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "file.txt\n"}}}})
	chunk("user_message_chunk", "second question")
	chunk("agent_message_chunk", "second answer")
	if os.Getenv("FAKEACP_LOAD_ERROR") != "" {
		a.replyError(message.ID, -32002, "session not found")
		return
	}
	a.write(map[string]any{"id": message.ID, "result": nil})
}

func (a *fakeACPAgent) list(message fakeACPMessage) {
	var params struct {
		CWD    string `json:"cwd"`
		Cursor string `json:"cursor"`
	}
	json.Unmarshal(message.Params, &params)
	pages := 1
	if value := os.Getenv("FAKEACP_LIST_PAGES"); value != "" {
		fmt.Sscanf(value, "%d", &pages)
	}
	page := 0
	if params.Cursor != "" {
		fmt.Sscanf(params.Cursor, "p%d", &page)
	}
	var sessions []any
	for k := 0; k < 2; k++ {
		sessions = append(sessions, map[string]any{
			"sessionId": fmt.Sprintf("s%d-%d", page, k),
			"cwd":       params.CWD,
			"title":     fmt.Sprintf("session %d-%d", page, k),
			"updatedAt": fmt.Sprintf("2026-01-%02dT00:00:00Z", page*2+k+1),
		})
	}
	result := map[string]any{"sessions": sessions}
	if page+1 < pages {
		result["nextCursor"] = fmt.Sprintf("p%d", page+1)
	}
	a.reply(message.ID, result)
}

func (a *fakeACPAgent) waitCancel(timeout time.Duration) bool {
	select {
	case <-a.cancel:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (a *fakeACPAgent) permissionRequest(session string) fakeACPMessage {
	return a.request("session/request_permission", map[string]any{
		"sessionId": session,
		"toolCall": map[string]any{
			"toolCallId": "p1", "title": "Write /tmp/x", "kind": "edit", "status": "pending",
			"rawInput": map[string]any{"file_path": "/tmp/x"},
		},
		"options": []any{
			map[string]any{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
			map[string]any{"optionId": "allow-with-updates", "name": "Always allow", "kind": "allow_always"},
			map[string]any{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
		},
	})
}

func permissionOutcomeText(response fakeACPMessage) string {
	var result struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	json.Unmarshal(response.Result, &result)
	return result.Outcome.Outcome + ":" + result.Outcome.OptionID
}

func (a *fakeACPAgent) prompt(message fakeACPMessage) {
	var params struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Text string `json:"text"`
		} `json:"prompt"`
	}
	json.Unmarshal(message.Params, &params)
	text := ""
	if len(params.Prompt) > 0 {
		text = params.Prompt[0].Text
	}
	session := params.SessionID
	done := func(reason string) { a.reply(message.ID, map[string]any{"stopReason": reason}) }
	switch {
	case text == "happy":
		for _, part := range []string{"think1", "think2"} {
			a.update(session, map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": part}})
		}
		a.message(session, "Hello ")
		a.message(session, "world")
		done("end_turn")
	case text == "split":
		// 連續 chunk：不夾 tool，Lumen 必須接成同一個 assistant block。
		a.message(session, "還")
		a.message(session, "沒驗證：")
		a.message(session, "go test 尚未跑")
		done("end_turn")
	case text == "interleave":
		// adapter 真的送 text -> tool -> text：中間的 tool 是邊界，兩段文字各自成 block。
		a.message(session, "還")
		a.update(session, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "ti", "title": "go test", "kind": "execute", "status": "pending"})
		a.update(session, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "ti", "status": "completed"})
		a.message(session, "沒驗證：")
		done("end_turn")
	case text == "tools":
		a.update(session, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "ls -la", "kind": "execute", "status": "pending", "rawInput": map[string]any{}})
		a.update(session, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "in_progress", "rawInput": map[string]any{"command": "ls -la"}})
		a.update(session, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1",
			"content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "a.txt\n"}}}})
		a.update(session, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed"})
		a.update(session, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t2", "title": "Write x", "kind": "edit", "status": "pending"})
		a.update(session, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t2", "status": "failed"})
		done("end_turn")
	case text == "meta":
		a.update(session, map[string]any{"sessionUpdate": "plan", "entries": []any{map[string]any{"content": "step one", "priority": "high", "status": "in_progress"}}})
		a.update(session, map[string]any{"sessionUpdate": "usage_update", "used": 1200, "size": 200000})
		a.update(session, map[string]any{"sessionUpdate": "usage_update", "used": 1500, "size": 200000, "cost": map[string]any{"amount": 0.125, "currency": "USD"}})
		a.update(session, map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": []any{map[string]any{"name": "compact", "description": "Compact the conversation"}}})
		a.update(session, map[string]any{"sessionUpdate": "session_info_update", "title": "Named by agent", "updatedAt": "2026-10-14T00:00:00Z"})
		a.update(session, map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": "acceptEdits"})
		a.update(session, map[string]any{"sessionUpdate": "future_update_kind"})
		a.update(session, map[string]any{"sessionUpdate": "future_update_kind"})
		a.update(session, map[string]any{"sessionUpdate": "another_future_kind"})
		a.message(session, "meta done")
		done("end_turn")
	case text == "modelpush":
		a.setModel("sonnet")
		a.update(session, map[string]any{"sessionUpdate": "config_option_update", "configOptions": a.configOptions()})
		a.message(session, "model pushed")
		done("end_turn")
	case text == "effortpush":
		a.setEffort("xhigh")
		a.update(session, map[string]any{"sessionUpdate": "config_option_update", "configOptions": a.configOptions()})
		a.message(session, "effort pushed")
		done("end_turn")
	case text == "configpush":
		a.setConfigValue("model", "sonnet")
		a.setConfigValue("effort", "xhigh")
		a.setConfigValue("style", "loud")
		a.update(session, map[string]any{"sessionUpdate": "config_option_update", "configOptions": a.configOptions()})
		a.message(session, "config pushed")
		done("end_turn")
	case text == "effortdrop":
		a.modelMu.Lock()
		a.noEffort = true
		a.modelMu.Unlock()
		a.update(session, map[string]any{"sessionUpdate": "config_option_update", "configOptions": a.configOptions()})
		a.message(session, "effort dropped")
		done("end_turn")
	case text == "perm":
		a.message(session, "outcome:"+permissionOutcomeText(a.permissionRequest(session)))
		done("end_turn")
	case text == "permcancel":
		a.message(session, "outcome:"+permissionOutcomeText(a.permissionRequest(session)))
		a.waitCancel(5 * time.Second)
		done("cancelled")
	case text == "permother":
		response := a.request("session/request_permission", map[string]any{
			"sessionId": "some-other-session",
			"toolCall":  map[string]any{"toolCallId": "p9", "title": "Write /tmp/y", "kind": "edit"},
			"options":   []any{map[string]any{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"}},
		})
		a.message(session, "outcome:"+permissionOutcomeText(response))
		done("end_turn")
	case text == "permempty":
		response := a.request("session/request_permission", map[string]any{
			"sessionId": session,
			"toolCall":  map[string]any{"toolCallId": "p8", "title": "Write", "kind": "edit"},
			"options":   []any{},
		})
		a.message(session, "outcome:"+permissionOutcomeText(response))
		done("end_turn")
	case text == "slow":
		a.message(session, "start")
		a.update(session, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "slow1", "title": "sleep 100", "kind": "execute", "status": "in_progress"})
		if os.Getenv("FAKEACP_IGNORE_CANCEL") == "1" {
			time.Sleep(30 * time.Second)
			done("cancelled")
			return
		}
		a.waitCancel(10 * time.Second)
		a.message(session, "late")
		done("cancelled")
	case text == "crash":
		fmt.Fprintln(os.Stderr, "fatal: boom")
		os.Exit(3)
	case text == "rpcerror":
		a.replyError(message.ID, -32603, "internal agent failure")
	case strings.HasPrefix(text, "stop:"):
		done(strings.TrimPrefix(text, "stop:"))
	default:
		a.message(session, "echo:"+text)
		done("end_turn")
	}
}

// ---- 測試端 helper ----

type fakeACPRun struct {
	backend *acpBackend
	events  <-chan Event
	logPath string
	cfg     acpConfig
}

// fakeACPConfig 回傳以 fake agent 當 command 的 acpConfig；env 設定 agent 行為。
func fakeACPConfig(t *testing.T, env map[string]string) (acpConfig, string) {
	t.Helper()
	logPath := t.TempDir() + "/fakeacp.log"
	t.Setenv(fakeACPFlag, "1")
	t.Setenv(fakeACPLog, logPath)
	for key, value := range env {
		t.Setenv(key, value)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return acpConfig{
		Name:     "fake",
		Command:  "fake-acp-agent",
		Args:     []string{"-test.run=^TestFakeACPAgentProcess$", "--", "--token=SEKRIT-VALUE"},
		StateDir: t.TempDir() + "/state",
		LookPath: func(string) (string, error) { return self, nil },
	}, logPath
}

func startFakeACP(t *testing.T, env map[string]string, tweak func(*acpConfig)) *fakeACPRun {
	t.Helper()
	cfg, logPath := fakeACPConfig(t, env)
	if tweak != nil {
		tweak(&cfg)
	}
	backend := newACPBackend(cfg)
	t.Cleanup(backend.Close)
	if err := backend.Open(t.Context()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	return &fakeACPRun{backend: backend, events: backend.Events(), logPath: logPath, cfg: cfg}
}

// readFakeACPLog 回傳 agent 記錄的所有訊息（含 start 記錄）。
func readFakeACPLog(t *testing.T, path string) []map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fake log: %v", err)
	}
	var entries []map[string]json.RawMessage
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &entry) == nil {
			entries = append(entries, entry)
		}
	}
	return entries
}

// fakeACPRequests 回傳 agent 收到的訊息中 method 為 method 的 params 清單。
func fakeACPRequests(t *testing.T, path, method string) []json.RawMessage {
	t.Helper()
	var out []json.RawMessage
	for _, entry := range readFakeACPLog(t, path) {
		raw, ok := entry["recv"]
		if !ok {
			continue
		}
		var message fakeACPMessage
		if json.Unmarshal(raw, &message) == nil && message.Method == method {
			out = append(out, message.Params)
		}
	}
	return out
}

// eventsUntil 讀 events 直到 stop 回傳 true，回傳沿途所有 event（含最後一個）。
func eventsUntil(t *testing.T, events <-chan Event, stop func(Event) bool) []Event {
	t.Helper()
	var all []Event
	deadline := time.After(10 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("event channel closed; got %#v", all)
			}
			all = append(all, event)
			if stop(event) {
				return all
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event; got %#v", all)
		}
	}
}

func isTurnCompleted(event Event) bool {
	_, ok := event.(EvTurnCompleted)
	return ok
}

// runTurn 送出 prompt 並收集到 EvTurnCompleted 為止的 events。
func (r *fakeACPRun) runTurn(t *testing.T, sessionID, text string) []Event {
	t.Helper()
	if _, err := r.backend.Prompt(sessionID, text, runtimeOverrides{}); err != nil {
		t.Fatalf("Prompt(%q) error = %v", text, err)
	}
	return eventsUntil(t, r.events, isTurnCompleted)
}
