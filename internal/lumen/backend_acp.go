package lumen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	acpProtocolVersion      = 1
	acpInitTimeout          = 30 * time.Second
	acpSessionTimeout       = 60 * time.Second
	acpDefaultCancelWait    = 10 * time.Second
	acpDefaultPermTimeout   = 15 * time.Minute
	acpMaxListPages         = 50
	claudeACPInstallHint    = "npm i -g @agentclientprotocol/claude-agent-acp@0.90.0"
	acpAuthRequiredCode     = -32000
	acpResourceNotFoundCode = -32002
)

// acpConfig 描述一個 ACP agent subprocess。P2 只有內部 constructor；使用者設定在 P3。
type acpConfig struct {
	Name        string // backend 名稱（顯示用）
	Command     string // 以 LookPath 解析的 bin；不使用 npx
	Args        []string
	InstallHint string // 找不到 Command 時顯示的安裝指令
	// SessionMeta 會原樣放進 session/new、session/load、session/resume 的 _meta。
	SessionMeta map[string]any
	// StateDir 是 subprocess 的固定 cmd.Dir；空字串使用 lumenStateDir()。
	StateDir string

	LookPath          func(string) (string, error) // nil 使用 exec.LookPath
	Environ           func() []string              // nil 使用 os.Environ
	PermissionTimeout time.Duration                // 0 使用預設
	CancelTimeout     time.Duration                // 0 使用預設
}

// claudeACPConfig 是內建的 Claude Code backend：全域安裝的 claude-agent-acp（不自動下載、不用 npx）。
// allowDangerouslySkipPermissions:false 只限制（把 bypassPermissions 從 availableModes 移除），
// 不是 mode 變更要求。
func claudeACPConfig() acpConfig {
	return acpConfig{
		Name:        "claude",
		Command:     "claude-agent-acp",
		InstallHint: claudeACPInstallHint,
		SessionMeta: map[string]any{
			"claudeCode": map[string]any{
				"options": map[string]any{"allowDangerouslySkipPermissions": false},
			},
		},
	}
}

type acpPermission struct {
	rpcID   json.RawMessage
	options map[string]bool
	timer   *time.Timer
}

type acpTurn struct {
	id               string
	reasoningOpen    bool
	reasoningStarted bool
	cancelling       bool
	cancelTimer      *time.Timer
}

// acpBackend 是 ACP client：Lumen 對 agent subprocess 講 Agent Client Protocol。
// 所有 session/update 與 agent request 在 rpcConn 的 read loop 內依線路順序同步處理，
// 因此 response 到達時，先前的 update 一定已轉成 event。
type acpBackend struct {
	cfg acpConfig

	events       chan Event
	done         chan struct{}
	sendMu       sync.RWMutex
	eventsClosed bool
	closeOnce    sync.Once
	closing      atomic.Bool
	waitOnce     sync.Once
	waitErr      error

	mu           sync.Mutex
	conn         *rpcConn
	cmd          *exec.Cmd
	stderr       *stderrRing
	caps         Capabilities
	canLoad      bool
	canResume    bool
	canList      bool
	disconnected bool
	failure      error

	sessionID string
	turnSeq   int
	turn      *acpTurn
	tools     map[string]*acpTool
	perms     map[string]*acpPermission
	replay    *acpReplay
	mode      string
	unknown   map[string]int
}

var _ Backend = (*acpBackend)(nil)

func newACPBackend(cfg acpConfig) *acpBackend {
	return &acpBackend{
		cfg:     cfg,
		events:  make(chan Event, 256),
		done:    make(chan struct{}),
		tools:   map[string]*acpTool{},
		perms:   map[string]*acpPermission{},
		unknown: map[string]int{},
	}
}

func (b *acpBackend) Name() string { return b.cfg.Name }

func (b *acpBackend) Caps() Capabilities {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.caps
}

func (b *acpBackend) Events() <-chan Event { return b.events }

func (b *acpBackend) Disconnected() (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.disconnected, b.failure
}

// unknownUpdateCount 回傳被忽略的未知 sessionUpdate 次數（供測試與診斷）。
func (b *acpBackend) unknownUpdateCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	total := 0
	for _, count := range b.unknown {
		total += count
	}
	return total
}

// ---- lifecycle ----

func (b *acpBackend) Open(ctx context.Context) error {
	err := b.open(ctx)
	if err != nil {
		b.mu.Lock()
		b.disconnected = true
		b.failure = err
		b.mu.Unlock()
		b.killProcess()
	}
	return err
}

func (b *acpBackend) open(ctx context.Context) error {
	lookPath := b.cfg.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	binary, err := lookPath(b.cfg.Command)
	if err != nil {
		message := fmt.Sprintf("backend %s: %s not found", b.cfg.Name, filepath.Base(b.cfg.Command))
		if b.cfg.InstallHint != "" {
			message += " · install: " + b.cfg.InstallHint
		}
		return errors.New(message)
	}
	stateDir := b.cfg.StateDir
	if stateDir == "" {
		if stateDir, err = lumenStateDir(); err != nil {
			return fmt.Errorf("backend %s: resolve state dir: %w", b.cfg.Name, err)
		}
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("backend %s: create state dir: %w", b.cfg.Name, err)
	}

	environ := os.Environ
	if b.cfg.Environ != nil {
		environ = b.cfg.Environ
	}
	ring := &stderrRing{}
	cmd := exec.Command(binary, b.cfg.Args...)
	cmd.Dir = stateDir
	cmd.Env = scrubACPEnv(environ())
	cmd.Stderr = ring
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("backend %s: open stdin: %w", b.cfg.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("backend %s: open stdout: %w", b.cfg.Name, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("backend %s: start %s: %v", b.cfg.Name, filepath.Base(binary), unwrapExecError(err))
	}

	conn := &rpcConn{jsonrpc: true}
	conn.init("acp agent", stdin, stdout)
	conn.handle = b.handleInbound
	conn.onClose = b.onConnClosed
	b.mu.Lock()
	b.conn, b.cmd, b.stderr = conn, cmd, ring
	b.mu.Unlock()
	go conn.readLoop()

	initCtx, cancel := context.WithTimeout(ctx, acpInitTimeout)
	defer cancel()
	var result struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			LoadSession         bool `json:"loadSession"`
			SessionCapabilities struct {
				List   json.RawMessage `json:"list"`
				Resume json.RawMessage `json:"resume"`
			} `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
		AuthMethods []json.RawMessage `json:"authMethods"`
	}
	if err := conn.call(initCtx, acpInitTimeout, "initialize", map[string]any{
		"protocolVersion":    acpProtocolVersion,
		"clientCapabilities": map[string]any{},
		"clientInfo":         map[string]any{"name": appName, "title": "Lumen", "version": appVersion},
	}, &result); err != nil {
		return b.initFailure(err)
	}
	if result.ProtocolVersion != acpProtocolVersion {
		return fmt.Errorf("backend %s speaks ACP protocol version %d; lumen requires %d", b.cfg.Name, result.ProtocolVersion, acpProtocolVersion)
	}
	if len(result.AuthMethods) > 0 {
		return b.loginRequired()
	}

	caps := result.AgentCapabilities
	b.mu.Lock()
	b.canLoad = caps.LoadSession
	b.canResume = capabilityPresent(caps.SessionCapabilities.Resume)
	b.canList = capabilityPresent(caps.SessionCapabilities.List)
	b.caps = Capabilities{
		SessionList:    b.canList,
		SessionResume:  b.canLoad || b.canResume,
		ReplayOnResume: b.canLoad,
		SlashCatalog:   "dynamic",
	}
	b.mu.Unlock()
	return nil
}

func capabilityPresent(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null" && value != "false"
}

func unwrapExecError(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

func (b *acpBackend) loginRequired() error {
	return fmt.Errorf("backend %s requires login: sign in with the agent's own CLI in another terminal, then retry", b.cfg.Name)
}

// initFailure 把 initialize 失敗（含連線中斷）轉成使用者可讀的錯誤，並附上 stderr 摘要。
func (b *acpBackend) initFailure(err error) error {
	var rpcErr *rpcCallError
	if errors.As(err, &rpcErr) {
		return fmt.Errorf("backend %s: %w", b.cfg.Name, err)
	}
	return fmt.Errorf("backend %s: initialize failed: %v%s", b.cfg.Name, err, b.stderrSummary())
}

func (b *acpBackend) stderrSummary() string {
	b.mu.Lock()
	ring := b.stderr
	b.mu.Unlock()
	if ring == nil {
		return ""
	}
	lines := ring.Tail(acpStderrShowLines)
	if len(lines) == 0 {
		return ""
	}
	return " · stderr: " + strings.Join(lines, " | ")
}

// wait 只會對 subprocess 呼叫一次 cmd.Wait，其餘呼叫共用結果。
func (b *acpBackend) wait() error {
	b.waitOnce.Do(func() {
		b.mu.Lock()
		cmd := b.cmd
		b.mu.Unlock()
		if cmd != nil {
			b.waitErr = cmd.Wait()
		}
	})
	return b.waitErr
}

func (b *acpBackend) killProcess() {
	b.closing.Store(true)
	b.mu.Lock()
	conn, cmd := b.conn, b.cmd
	b.mu.Unlock()
	if conn != nil {
		conn.closeTransport()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		go b.wait()
	}
}

func (b *acpBackend) Close() {
	b.closeOnce.Do(func() {
		b.closing.Store(true)
		close(b.done)
		b.mu.Lock()
		b.disconnected = true
		for id, perm := range b.perms {
			if perm.timer != nil {
				perm.timer.Stop()
			}
			delete(b.perms, id)
		}
		if b.turn != nil && b.turn.cancelTimer != nil {
			b.turn.cancelTimer.Stop()
		}
		b.mu.Unlock()
		b.killProcess()
		b.closeEvents()
	})
}

// onConnClosed 在 read loop 結束後執行：非使用者主動 Close 時，把進行中的 turn 標為 interrupted，
// 再送 EvConnectionLost（含 stderr 最後幾行），最後關閉 Events。
func (b *acpBackend) onConnClosed(readErr error) {
	if b.closing.Load() {
		return
	}
	go func() {
		b.mu.Lock()
		turn := b.turn
		b.turn = nil
		if turn != nil && turn.cancelTimer != nil {
			turn.cancelTimer.Stop()
		}
		for id, perm := range b.perms {
			if perm.timer != nil {
				perm.timer.Stop()
			}
			delete(b.perms, id)
		}
		b.mu.Unlock()

		exitErr := b.wait()
		reason := readErr
		if reason == nil {
			if exitErr != nil {
				reason = fmt.Errorf("%s exited: %v", b.cfg.Name, exitErr)
			} else {
				reason = fmt.Errorf("%s agent closed the connection", b.cfg.Name)
			}
		}
		if summary := b.stderrSummary(); summary != "" {
			reason = fmt.Errorf("%v%s", reason, summary)
		}
		b.mu.Lock()
		b.disconnected = true
		b.failure = reason
		b.mu.Unlock()

		if turn != nil {
			b.closeReasoning(turn)
			b.send(EvTurnCompleted{TurnID: turn.id, Status: TurnInterrupted, Label: "interrupted"})
		}
		b.send(EvConnectionLost{Err: reason})
		b.closeEvents()
	}()
}

func (b *acpBackend) closeReasoning(turn *acpTurn) {
	b.mu.Lock()
	open := turn.reasoningOpen
	turn.reasoningOpen = false
	b.mu.Unlock()
	if open {
		b.send(EvReasoningCompleted{TurnID: turn.id})
	}
}

func (b *acpBackend) send(event Event) bool {
	b.sendMu.RLock()
	defer b.sendMu.RUnlock()
	if b.eventsClosed {
		return false
	}
	select {
	case b.events <- event:
		return true
	case <-b.done:
		return false
	}
}

func (b *acpBackend) sendAll(events []Event) {
	for _, event := range events {
		if !b.send(event) {
			return
		}
	}
}

func (b *acpBackend) closeEvents() {
	b.sendMu.Lock()
	defer b.sendMu.Unlock()
	if !b.eventsClosed {
		b.eventsClosed = true
		close(b.events)
	}
}

func (b *acpBackend) connection() (*rpcConn, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn == nil {
		return nil, fmt.Errorf("backend %s is not connected", b.cfg.Name)
	}
	return b.conn, nil
}

// ---- sessions ----

func (b *acpBackend) sessionParams(cwd string, extra map[string]any) (map[string]any, error) {
	if cwd == "" {
		return nil, errors.New("session cwd is required")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	params := map[string]any{"cwd": abs, "mcpServers": []any{}}
	for key, value := range extra {
		params[key] = value
	}
	if b.cfg.SessionMeta != nil {
		params["_meta"] = b.cfg.SessionMeta
	}
	return params, nil
}

type acpSessionResult struct {
	SessionID string `json:"sessionId"`
	Modes     *struct {
		CurrentModeID string `json:"currentModeId"`
	} `json:"modes"`
	ConfigOptions []acpConfigOption `json:"configOptions"`
	Models        *struct {
		CurrentModelID string `json:"currentModelId"`
	} `json:"models"`
}

func (r acpSessionResult) mode() string {
	current := ""
	if r.Modes != nil {
		current = r.Modes.CurrentModeID
	}
	return acpModeOf(current, r.ConfigOptions)
}

func (r acpSessionResult) model() string {
	if r.Models != nil {
		return r.Models.CurrentModelID
	}
	return ""
}

func (b *acpBackend) NewSession(cwd string, rt runtimeOverrides) (SessionInfo, error) {
	conn, err := b.connection()
	if err != nil {
		return SessionInfo{}, err
	}
	params, err := b.sessionParams(cwd, nil)
	if err != nil {
		return SessionInfo{}, err
	}
	var result acpSessionResult
	if err := conn.call(context.Background(), acpSessionTimeout, "session/new", params, &result); err != nil {
		return SessionInfo{}, b.sessionError(err)
	}
	if result.SessionID == "" {
		return SessionInfo{}, fmt.Errorf("backend %s returned no sessionId", b.cfg.Name)
	}
	b.adopt(result.SessionID, result.mode())
	return SessionInfo{ID: result.SessionID, CWD: params["cwd"].(string), Model: result.model()}, nil
}

// adopt 切換到新的 session：清掉前一個 session 的 turn / tool 狀態並更新 mode 警示。
func (b *acpBackend) adopt(sessionID, mode string) {
	b.mu.Lock()
	b.sessionID = sessionID
	b.turn = nil
	b.tools = map[string]*acpTool{}
	events := b.setModeLocked(mode)
	b.mu.Unlock()
	b.sendAll(events)
}

// sessionError 把 session 層的 JSON-RPC 錯誤轉成 Lumen 的錯誤：auth 錯誤 -> requires login。
func (b *acpBackend) sessionError(err error) error {
	var rpcErr *rpcCallError
	if errors.As(err, &rpcErr) {
		lower := strings.ToLower(rpcErr.Message)
		if rpcErr.Code == acpAuthRequiredCode || strings.Contains(lower, "auth") || strings.Contains(lower, "login") {
			return b.loginRequired()
		}
	}
	return err
}

func (b *acpBackend) ResumeSession(id, cwd string, rt runtimeOverrides) (SessionInfo, error) {
	conn, err := b.connection()
	if err != nil {
		return SessionInfo{}, err
	}
	b.mu.Lock()
	canLoad, canResume := b.canLoad, b.canResume
	b.mu.Unlock()
	params, err := b.sessionParams(cwd, map[string]any{"sessionId": id})
	if err != nil {
		return SessionInfo{}, err
	}

	method := "session/resume"
	switch {
	case canLoad:
		method = "session/load"
	case !canResume:
		return SessionInfo{}, fmt.Errorf("backend %s does not support resuming sessions", b.cfg.Name)
	}

	var replay *acpReplay
	b.mu.Lock()
	previous := b.sessionID
	b.sessionID = id
	if method == "session/load" {
		replay = newACPReplay(id)
		b.replay = replay
	}
	b.mu.Unlock()

	var result acpSessionResult
	err = conn.call(context.Background(), acpSessionTimeout, method, params, &result)

	b.mu.Lock()
	b.replay = nil
	if err != nil {
		b.sessionID = previous
	}
	b.mu.Unlock()
	if err != nil {
		err = b.sessionError(err)
		var rpcErr *rpcCallError
		if errors.As(err, &rpcErr) && (rpcErr.Code == acpResourceNotFoundCode || strings.Contains(strings.ToLower(rpcErr.Message), "not found")) {
			return SessionInfo{}, fmt.Errorf("%w: %v", errSessionNotFound, err)
		}
		return SessionInfo{}, err
	}
	b.adopt(id, result.mode())
	info := SessionInfo{ID: id, CWD: params["cwd"].(string), Model: result.model()}
	if replay != nil {
		info.Turns = replay.history()
	}
	return info, nil
}

func (b *acpBackend) ListSessions(cwd string, limit int) ([]SessionInfo, error) {
	b.mu.Lock()
	canList := b.canList
	b.mu.Unlock()
	if !canList {
		return nil, fmt.Errorf("backend %s does not support listing sessions", b.cfg.Name)
	}
	conn, err := b.connection()
	if err != nil {
		return nil, err
	}
	if cwd == "" {
		if cwd, err = os.Getwd(); err != nil {
			return nil, err
		}
	}
	if cwd, err = filepath.Abs(cwd); err != nil {
		return nil, err
	}

	var sessions []SessionInfo
	cursor := ""
	for page := 0; page < acpMaxListPages; page++ {
		params := map[string]any{"cwd": cwd}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result struct {
			Sessions []struct {
				SessionID string `json:"sessionId"`
				CWD       string `json:"cwd"`
				Title     string `json:"title"`
				UpdatedAt string `json:"updatedAt"`
			} `json:"sessions"`
			NextCursor string `json:"nextCursor"`
		}
		if err := conn.call(context.Background(), acpSessionTimeout, "session/list", params, &result); err != nil {
			return nil, err
		}
		for _, item := range result.Sessions {
			sessions = append(sessions, SessionInfo{
				ID: item.SessionID, CWD: valueOr(item.CWD, cwd), Name: item.Title, Preview: item.Title, UpdatedAt: item.UpdatedAt,
			})
		}
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		return fmt.Sprint(sessions[i].UpdatedAt) > fmt.Sprint(sessions[j].UpdatedAt)
	})
	if limit > 0 && len(sessions) > limit {
		sessions = sessions[:limit]
	}
	return sessions, nil
}

// ---- prompt / cancel ----

func (b *acpBackend) Prompt(sessionID, text string, rt runtimeOverrides) (string, error) {
	conn, err := b.connection()
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	if b.turn != nil {
		b.mu.Unlock()
		return "", errors.New("a turn is already running")
	}
	b.turnSeq++
	turn := &acpTurn{id: fmt.Sprintf("turn-%d", b.turnSeq)}
	b.turn = turn
	b.sessionID = sessionID
	b.tools = map[string]*acpTool{}
	b.mu.Unlock()

	b.send(EvTurnStarted{SessionID: sessionID, TurnID: turn.id})
	go b.runPrompt(conn, turn, sessionID, text)
	return turn.id, nil
}

func (b *acpBackend) runPrompt(conn *rpcConn, turn *acpTurn, sessionID, text string) {
	var result struct {
		StopReason string `json:"stopReason"`
	}
	err := conn.call(context.Background(), 0, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    []any{map[string]any{"type": "text", "text": text}},
	}, &result)

	select {
	case <-conn.closed:
		// 連線結束由 onConnClosed 負責把 turn 標成 interrupted 並送 EvConnectionLost。
		return
	default:
	}
	b.mu.Lock()
	if b.turn != turn {
		b.mu.Unlock()
		return
	}
	b.turn = nil
	if turn.cancelTimer != nil {
		turn.cancelTimer.Stop()
	}
	b.mu.Unlock()

	b.closeReasoning(turn)
	event := EvTurnCompleted{TurnID: turn.id}
	if err != nil {
		event.Status, event.Label, event.Err = TurnFailed, "failed", err
	} else {
		switch result.StopReason {
		case "end_turn":
			event.Status, event.Label = TurnCompleted, "completed"
		case "cancelled":
			event.Status, event.Label = TurnInterrupted, "interrupted"
		case "refusal":
			event.Status, event.Label = TurnRefused, "refused"
		case "max_tokens":
			event.Status, event.Label = TurnMaxTokens, "max tokens"
		case "max_turn_requests":
			event.Status, event.Label = TurnMaxTokens, "max turn requests"
		default:
			event.Status, event.Label = TurnCompleted, valueOr(result.StopReason, "completed")
		}
	}
	b.send(event)
}

// Cancel 先把所有 pending permission 回 cancelled，再送 session/cancel，並把未完成 tool 標 cancelled。
// agent 在 cancelTimeout 內沒有結束 turn 時顯示 notice。
func (b *acpBackend) Cancel(sessionID, turnID string) error {
	conn, err := b.connection()
	if err != nil {
		return err
	}
	b.mu.Lock()
	turn := b.turn
	if turn == nil {
		b.mu.Unlock()
		return nil
	}
	turn.cancelling = true
	pending := b.takePermissionsLocked()
	var events []Event
	for _, tool := range b.tools {
		if tool.started && !tool.finished {
			tool.finished = true
			events = append(events, EvToolCompleted{ToolID: tool.id, Status: ToolCancelled, Label: "cancelled"})
		}
	}
	timeout := b.cfg.CancelTimeout
	if timeout <= 0 {
		timeout = acpDefaultCancelWait
	}
	if turn.cancelTimer != nil {
		turn.cancelTimer.Stop()
	}
	turn.cancelTimer = time.AfterFunc(timeout, func() {
		b.mu.Lock()
		stuck := b.turn == turn
		b.mu.Unlock()
		if stuck {
			b.send(EvNotice{Level: NoticeWarn, Text: "agent not responding to cancel"})
		}
	})
	sid := valueOr(sessionID, b.sessionID)
	b.mu.Unlock()

	for _, perm := range pending {
		_ = conn.respond(perm.rpcID, acpCancelledOutcome())
	}
	err = conn.notify("session/cancel", map[string]any{"sessionId": sid})
	b.sendAll(events)
	return err
}

func acpCancelledOutcome() map[string]any {
	return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
}

func (b *acpBackend) takePermissionsLocked() []*acpPermission {
	var out []*acpPermission
	for id, perm := range b.perms {
		if perm.timer != nil {
			perm.timer.Stop()
		}
		out = append(out, perm)
		delete(b.perms, id)
	}
	return out
}

// ---- permission ----

func (b *acpBackend) ResolveApproval(reqID, optionID string) error {
	conn, err := b.connection()
	if err != nil {
		return err
	}
	b.mu.Lock()
	perm := b.perms[reqID]
	delete(b.perms, reqID)
	if perm != nil && perm.timer != nil {
		perm.timer.Stop()
	}
	b.mu.Unlock()
	if perm == nil {
		return fmt.Errorf("permission request %s is no longer pending", reqID)
	}
	if optionID == CancelApprovalID {
		return conn.respond(perm.rpcID, acpCancelledOutcome())
	}
	if !perm.options[optionID] {
		_ = conn.respond(perm.rpcID, acpCancelledOutcome())
		return fmt.Errorf("option %q is not offered by this permission request", optionID)
	}
	return conn.respond(perm.rpcID, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}})
}

// RejectRequest 對 permission request 一律回 cancelled（不是 error），其餘 request 回 JSON-RPC error。
func (b *acpBackend) RejectRequest(reqID, message string) error {
	conn, err := b.connection()
	if err != nil {
		return err
	}
	b.mu.Lock()
	perm := b.perms[reqID]
	delete(b.perms, reqID)
	if perm != nil && perm.timer != nil {
		perm.timer.Stop()
	}
	b.mu.Unlock()
	if perm != nil {
		return conn.respond(perm.rpcID, acpCancelledOutcome())
	}
	return conn.respondError(json.RawMessage(reqID), -32001, message)
}

func (b *acpBackend) AnswerInput(string, map[string][]string) error {
	return errors.New("backend does not support user input requests")
}

// ---- inbound (agent -> client) ----

func (b *acpBackend) handleInbound(message rpcMessage) {
	isRequest := len(message.ID) > 0 && string(message.ID) != "null"
	defer func() {
		if recover() != nil {
			if isRequest {
				if conn, err := b.connection(); err == nil {
					_ = conn.respondError(message.ID, -32603, "lumen: internal error")
				}
			}
			b.send(EvNotice{Level: NoticeWarn, Text: "agent message dropped: internal error"})
		}
	}()
	switch {
	case isRequest && message.Method == "session/request_permission":
		b.handlePermission(message)
	case isRequest:
		// 第一個 slice 不宣告 fs / terminal / elicitation，其餘 request 一律不支援。
		if conn, err := b.connection(); err == nil {
			_ = conn.respondError(message.ID, -32601, "method not supported by lumen: "+message.Method)
		}
	case message.Method == "session/update":
		b.handleUpdate(message.Params)
	}
}

func (b *acpBackend) handlePermission(message rpcMessage) {
	conn, err := b.connection()
	if err != nil {
		return
	}
	cancel := func() { _ = conn.respond(message.ID, acpCancelledOutcome()) }
	var params struct {
		SessionID string `json:"sessionId"`
		ToolCall  struct {
			ToolCallID string          `json:"toolCallId"`
			Title      string          `json:"title"`
			Kind       string          `json:"kind"`
			RawInput   json.RawMessage `json:"rawInput"`
		} `json:"toolCall"`
		Options []struct {
			OptionID string `json:"optionId"`
			Name     string `json:"name"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if json.Unmarshal(message.Params, &params) != nil {
		cancel()
		return
	}

	b.mu.Lock()
	turn := b.turn
	switch {
	case params.SessionID == "" || params.SessionID != b.sessionID,
		turn == nil,
		turn.cancelling,
		len(params.Options) == 0:
		b.mu.Unlock()
		cancel()
		return
	}
	options := make([]ApprovalOption, 0, len(params.Options))
	allowed := map[string]bool{}
	for _, option := range params.Options {
		if option.OptionID == "" {
			continue
		}
		allowed[option.OptionID] = true
		options = append(options, ApprovalOption{ID: option.OptionID, Label: valueOr(option.Name, option.OptionID), Kind: acpOptionKind(option.Kind)})
	}
	if len(options) == 0 {
		b.mu.Unlock()
		cancel()
		return
	}
	title, kind, command := params.ToolCall.Title, params.ToolCall.Kind, rawInputCommand(params.ToolCall.RawInput)
	if tool := b.tools[params.ToolCall.ToolCallID]; tool != nil {
		title, kind, command = valueOr(title, tool.title), valueOr(kind, tool.kind), valueOr(command, tool.command)
	}
	reqID := string(message.ID)
	perm := &acpPermission{rpcID: message.ID, options: allowed}
	timeout := b.cfg.PermissionTimeout
	if timeout <= 0 {
		timeout = acpDefaultPermTimeout
	}
	perm.timer = time.AfterFunc(timeout, func() {
		b.mu.Lock()
		current := b.perms[reqID]
		if current == perm {
			delete(b.perms, reqID)
		}
		b.mu.Unlock()
		if current == perm {
			cancel()
			b.send(EvApprovalExpired{ReqID: reqID, Text: "permission request timed out · cancelled"})
		}
	})
	b.perms[reqID] = perm
	turnID := turn.id
	b.mu.Unlock()

	b.send(EvApprovalRequest{
		ReqID:    reqID,
		TurnID:   turnID,
		Title:    acpToolTitle(kind, valueOr(title, "tool call"), command),
		Options:  options,
		Explicit: true,
	})
}

func acpOptionKind(kind string) ApprovalOptionKind {
	switch kind {
	case "allow_once":
		return OptionAllowOnce
	case "allow_always":
		return OptionAllowAlways
	case "reject_once":
		return OptionRejectOnce
	case "reject_always":
		return OptionRejectAlways
	}
	return OptionOther
}

func (b *acpBackend) handleUpdate(raw json.RawMessage) {
	var params struct {
		SessionID string    `json:"sessionId"`
		Update    acpUpdate `json:"update"`
	}
	if json.Unmarshal(raw, &params) != nil {
		return
	}
	update := params.Update

	b.mu.Lock()
	if params.SessionID != b.sessionID {
		b.mu.Unlock()
		return
	}
	if b.replay != nil {
		b.replay.add(update)
		b.mu.Unlock()
		return
	}
	events := b.translateLocked(update)
	b.mu.Unlock()
	b.sendAll(events)
}

// setModeLocked 更新 permission mode；警示文字改變時回傳 EvModeWarning。
func (b *acpBackend) setModeLocked(mode string) []Event {
	if mode == "" || mode == b.mode {
		return nil
	}
	previous := acpModeWarning(b.mode)
	b.mode = mode
	if warning := acpModeWarning(mode); warning != previous {
		return []Event{EvModeWarning{Mode: mode, Text: warning}}
	}
	return nil
}

// translateLocked 把 live session/update 轉成 events（呼叫端持有 b.mu）。
func (b *acpBackend) translateLocked(u acpUpdate) []Event {
	var events []Event
	turn := b.turn
	turnID := ""
	if turn != nil {
		turnID = turn.id
	}
	closeReasoning := func() {
		if turn != nil && turn.reasoningOpen {
			turn.reasoningOpen = false
			events = append(events, EvReasoningCompleted{TurnID: turnID})
		}
	}

	switch u.Kind {
	case "agent_message_chunk":
		closeReasoning()
		if text := textOf(u.Content); text != "" {
			events = append(events, EvAssistantDelta{TurnID: turnID, Text: text})
		}
	case "agent_thought_chunk":
		if turn != nil {
			if !turn.reasoningStarted {
				turn.reasoningStarted = true
				events = append(events, EvReasoningStarted{TurnID: turnID})
			}
			turn.reasoningOpen = true
		}
		events = append(events, EvReasoningDelta{TurnID: turnID, Text: textOf(u.Content)})
	case "user_message_chunk":
		// live 狀態下 user 訊息由 UI 自己顯示。
	case "tool_call", "tool_call_update":
		closeReasoning()
		events = append(events, b.translateToolLocked(u)...)
	case "plan":
		closeReasoning()
		items := make([]PlanItem, 0, len(u.Entries))
		for _, entry := range u.Entries {
			items = append(items, PlanItem{Text: entry.Content, Status: entry.Status})
		}
		events = append(events, EvPlanUpdated{Items: items})
	case "usage_update":
		if u.Used != nil {
			usage := EvUsage{Used: *u.Used, Size: u.Size}
			if u.Cost != nil {
				usage.Cost = &UsageCost{Amount: u.Cost.Amount, Currency: u.Cost.Currency}
			}
			events = append(events, usage)
		}
	case "available_commands_update":
		commands := make([]SlashCommand, 0, len(u.AvailableCommands))
		for _, command := range u.AvailableCommands {
			commands = append(commands, SlashCommand{Name: command.Name, Description: command.Description})
		}
		events = append(events, EvCommandsUpdated{Commands: commands})
	case "session_info_update":
		if u.Title != nil && *u.Title != "" {
			events = append(events, EvSessionTitle{SessionID: b.sessionID, Title: *u.Title})
		}
	case "current_mode_update":
		events = append(events, b.setModeLocked(u.CurrentModeID)...)
	case "config_option_update":
		events = append(events, b.setModeLocked(acpModeOf("", u.ConfigOptions))...)
	default:
		b.unknown[u.Kind]++
	}
	return events
}

func (b *acpBackend) translateToolLocked(u acpUpdate) []Event {
	if u.ToolCallID == "" {
		return nil
	}
	tool := b.tools[u.ToolCallID]
	if tool == nil {
		tool = &acpTool{id: u.ToolCallID}
		b.tools[u.ToolCallID] = tool
	}
	tool.merge(u)

	var events []Event
	if !tool.started {
		tool.started = true
		events = append(events, EvToolStarted{
			ToolID:  tool.id,
			Kind:    acpToolKind(tool.kind),
			Title:   tool.displayTitle(),
			Command: tool.command,
		})
	}
	if text := toolText(u.Content); text != "" && text != tool.output {
		delta := text
		if strings.HasPrefix(text, tool.output) {
			delta = text[len(tool.output):]
		}
		tool.output = text
		if delta != "" {
			events = append(events, EvToolOutput{ToolID: tool.id, Text: delta})
		}
	}
	if !tool.finished {
		switch tool.status {
		case "completed":
			tool.finished = true
			events = append(events, EvToolCompleted{ToolID: tool.id, Status: ToolOK})
		case "failed":
			tool.finished = true
			events = append(events, EvToolCompleted{ToolID: tool.id, Status: ToolFailed, Label: "failed"})
		}
	}
	return events
}
