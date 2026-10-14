package lumen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// fakeBackend 是沒有任何 capability 的 Backend，用來驗證 UI 的 fallback 路徑。
type fakeBackend struct {
	events  chan Event
	prompts []string
}

func (b *fakeBackend) Name() string                                  { return "fake" }
func (b *fakeBackend) Caps() Capabilities                            { return Capabilities{} }
func (b *fakeBackend) Events() <-chan Event                          { return b.events }
func (b *fakeBackend) Disconnected() (bool, error)                   { return false, nil }
func (b *fakeBackend) Open(context.Context) error                    { return nil }
func (b *fakeBackend) Close()                                        {}
func (b *fakeBackend) Cancel(string, string) error                   { return nil }
func (b *fakeBackend) RejectRequest(string, string) error            { return nil }
func (b *fakeBackend) ResolveApproval(string, string) error          { return nil }
func (b *fakeBackend) AnswerInput(string, map[string][]string) error { return nil }

func (b *fakeBackend) NewSession(string, runtimeOverrides) (SessionInfo, error) {
	return SessionInfo{ID: "fake-session"}, nil
}

func (b *fakeBackend) ResumeSession(id, _ string, _ runtimeOverrides) (SessionInfo, error) {
	return SessionInfo{ID: id}, nil
}

func (b *fakeBackend) ListSessions(string, int) ([]SessionInfo, error) { return nil, nil }

func (b *fakeBackend) Prompt(_, text string, _ runtimeOverrides) (string, error) {
	b.prompts = append(b.prompts, text)
	return "turn-1", nil
}

var _ Backend = (*fakeBackend)(nil)

// pipedCodex 把 appServer 接到 io.Pipe，讓測試扮演 codex 送出 JSON-RPC 行。
type pipedCodex struct {
	backend *codexBackend
	server  *io.PipeWriter
	stdin   *bytes.Buffer
}

func newPipedCodex(t *testing.T, sessionID string) *pipedCodex {
	t.Helper()
	reader, writer := io.Pipe()
	stdin := &bytes.Buffer{}
	client := newAppServer(&bufferWriteCloser{Buffer: stdin}, reader, &exec.Cmd{})
	go client.readLoop()
	backend := newCodexBackend(client, sessionID)
	t.Cleanup(backend.Close)
	return &pipedCodex{backend: backend, server: writer, stdin: stdin}
}

func (p *pipedCodex) send(t *testing.T, line string) {
	t.Helper()
	if _, err := fmt.Fprintln(p.server, line); err != nil {
		t.Fatalf("write codex line: %v", err)
	}
}

func nextEvent(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("event channel closed")
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("no event arrived")
		return nil
	}
}

// T-P1-03：server request 轉成 Event；id 不符時不送 approval，並回覆 codex error。
func TestCodexBackendRequestEvents(t *testing.T) {
	codex := newPipedCodex(t, "thread-1")
	events := codex.backend.Events()

	codex.send(t, `{"id":3,"method":"item/commandExecution/requestApproval","params":{"threadId":"thread-1","command":"touch x","availableDecisions":["accept","decline"]}}`)
	approval, ok := nextEvent(t, events).(EvApprovalRequest)
	if !ok || approval.ReqID != "3" || approval.Title != "touch x" || len(approval.Options) != 2 {
		t.Fatalf("approval event = %#v", approval)
	}

	codex.send(t, `{"id":4,"method":"item/commandExecution/requestApproval","params":{"threadId":"other-thread","command":"touch x"}}`)
	rejected, ok := nextEvent(t, events).(EvRequestRejected)
	if !ok || rejected.ReqID != "4" || !strings.Contains(rejected.Message, "thread mismatch") {
		t.Fatalf("rejection event = %#v", rejected)
	}
	var response struct {
		ID    int `json:"id"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(codex.stdin.Bytes()), &response); err != nil {
		t.Fatalf("decode rejection response %q: %v", codex.stdin.String(), err)
	}
	if response.ID != 4 || response.Error == nil || !strings.Contains(response.Error.Message, "thread mismatch") {
		t.Fatalf("rejection response = %#v", response)
	}

	codex.send(t, `{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"turn-1"}}}`)
	if started, ok := nextEvent(t, events).(EvTurnStarted); !ok || started.TurnID != "turn-1" {
		t.Fatalf("turn started event = %#v", started)
	}
	codex.send(t, `{"id":5,"method":"item/tool/requestUserInput","params":{"threadId":"thread-1","turnId":"turn-old","questions":[{"id":"q","question":"?"}]}}`)
	rejected, ok = nextEvent(t, events).(EvRequestRejected)
	if !ok || !strings.Contains(rejected.Message, "turn mismatch") {
		t.Fatalf("stale turn request event = %#v", rejected)
	}
	codex.send(t, `{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}}`)
	if _, ok := nextEvent(t, events).(EvTurnCompleted); !ok {
		t.Fatal("turn completed event missing")
	}
	// turn 結束後 backend 不再要求 turn id 相符。
	codex.send(t, `{"id":6,"method":"item/tool/requestUserInput","params":{"threadId":"thread-1","turnId":"turn-next","questions":[{"id":"q","question":"?"}]}}`)
	if input, ok := nextEvent(t, events).(EvInputRequest); !ok || input.ReqID != "6" {
		t.Fatalf("input request event = %#v", input)
	}
}

func TestCodexBackendReportsConnectionLostThenCloses(t *testing.T) {
	codex := newPipedCodex(t, "thread-1")
	events := codex.backend.Events()
	codex.send(t, `{"method":"warning","params":{"message":"last words"}}`)
	codex.server.Close()

	if notice, ok := nextEvent(t, events).(EvNotice); !ok || notice.Text != "last words" {
		t.Fatalf("queued notice was dropped: %#v", notice)
	}
	if _, ok := nextEvent(t, events).(EvConnectionLost); !ok {
		t.Fatal("connection lost event missing")
	}
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("events stayed open after connection lost")
		}
	case <-time.After(time.Second):
		t.Fatal("events were not closed")
	}
	if closed, _ := codex.backend.Disconnected(); !closed {
		t.Fatal("backend did not report disconnected")
	}
}

// T-P1-04：codex 依序送出 turn started、delta、tool、completed，UI 與重構前得到相同的 transcript。
func TestCodexBackendTurnRendersTranscript(t *testing.T) {
	codex := newPipedCodex(t, "thread-1")
	events := codex.backend.Events()
	frontend := &ui{
		backend:        codex.backend,
		thread:         threadSummary{ID: "thread-1"},
		screenMode:     true,
		busy:           true,
		showToolOutput: true,
	}
	frontend.beginScreenTurn("")
	for _, line := range []string{
		`{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"turn-1"}}}`,
		`{"method":"item/started","params":{"threadId":"thread-1","item":{"type":"commandExecution","id":"i1","command":"ls"}}}`,
		`{"method":"item/commandExecution/outputDelta","params":{"threadId":"thread-1","itemId":"i1","delta":"a.txt\n"}}`,
		`{"method":"item/completed","params":{"threadId":"thread-1","item":{"type":"commandExecution","id":"i1","status":"failed","exitCode":1}}}`,
		`{"method":"item/agentMessage/delta","params":{"threadId":"thread-1","delta":"done"}}`,
		`{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}}`,
	} {
		codex.send(t, line)
	}
	for count := 0; count < 6; count++ {
		frontend.handleEvent(nextEvent(t, events))
	}

	if frontend.busy || frontend.turnID != "" {
		t.Fatalf("turn did not finish: busy=%t turnID=%q", frontend.busy, frontend.turnID)
	}
	plain := stripANSI(frontend.screenFrame(100, 30))
	for _, expected := range []string{"$ ls", "a.txt", "failed exit 1", "done", "turn completed", "recap · completed"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("transcript missing %q: %q", expected, plain)
		}
	}
}

// T-P1-05：approval 只接受 availableDecisions 內的值，回覆格式為 {"decision": ...}。
func TestApprovalOnlyAcceptsAvailableDecisions(t *testing.T) {
	var rpcOutput bytes.Buffer
	frontend := &ui{
		backend:    newCodexBackend(newAppServer(&bufferWriteCloser{Buffer: &rpcOutput}, io.NopCloser(strings.NewReader("")), &exec.Cmd{}), "thread-1"),
		thread:     threadSummary{ID: "thread-1"},
		screenMode: true,
	}
	frontend.handleEvent(EvApprovalRequest{
		ReqID: "9",
		Title: "touch x",
		Options: []ApprovalOption{
			{ID: "accept", Label: "accept", Kind: OptionAllowOnce},
			{ID: "decline", Label: "decline", Kind: OptionRejectOnce},
		},
	})
	if err := frontend.handleApprovalKey(keyEvent{typ: keyRune, rune: 's'}); err != nil {
		t.Fatalf("unavailable decision error = %v", err)
	}
	if frontend.approval == nil || rpcOutput.Len() != 0 {
		t.Fatalf("unavailable decision was sent: approval=%#v output=%q", frontend.approval, rpcOutput.String())
	}
	if err := frontend.handleApprovalKey(keyEvent{typ: keyRune, rune: 'd'}); err != nil {
		t.Fatalf("decline error = %v", err)
	}
	var response struct {
		ID     int
		Result map[string]string
	}
	if err := json.Unmarshal(bytes.TrimSpace(rpcOutput.Bytes()), &response); err != nil {
		t.Fatalf("decode decision response %q: %v", rpcOutput.String(), err)
	}
	if response.ID != 9 || response.Result["decision"] != "decline" {
		t.Fatalf("decision response = %#v", response)
	}
}

// T-P1-06：capability 為 false 時顯示 fallback，且不把 slash text 當成 prompt 送出。
func TestUnsupportedCapabilityShowsFallbackWithoutPrompt(t *testing.T) {
	backend := &fakeBackend{events: make(chan Event)}
	frontend := &ui{
		backend:    backend,
		thread:     threadSummary{ID: "fake-session"},
		screenMode: true,
		cwd:        "/tmp/project",
	}
	for _, command := range []string{"/goal", "/goal ship it", "/compact", "/review", "/fork", "/model list", "/skills", "/hooks", "/mcp", "/apps", "/plugins", "/debug-config", "/archive", "/delete confirm"} {
		if err := frontend.handleCommand(command); err != nil {
			t.Fatalf("handleCommand(%q) error = %v", command, err)
		}
		name := strings.Fields(command)[0]
		expected := name + " is not supported by backend fake"
		if plain := stripANSI(frontend.screenFrame(100, 24)); !strings.Contains(plain, expected) {
			t.Fatalf("handleCommand(%q) did not show %q: %q", command, expected, plain)
		}
	}
	if len(backend.prompts) != 0 {
		t.Fatalf("slash command text was sent as prompt: %q", backend.prompts)
	}
}

// T-P1-10：reasoning started / completed 的 UI 狀態轉換；重複的 started 不得重複顯示 thinking row。
func TestReasoningEventsDriveThinkingState(t *testing.T) {
	var output bytes.Buffer
	frontend := &ui{out: &output}
	frontend.handleEvent(EvReasoningStarted{TurnID: "turn-1"})
	if !frontend.reasoningActive || !frontend.reasoningShown {
		t.Fatalf("reasoning state = active:%t shown:%t", frontend.reasoningActive, frontend.reasoningShown)
	}
	frontend.handleEvent(EvReasoningStarted{TurnID: "turn-1"})
	if strings.Count(output.String(), "reasoning") != 1 {
		t.Fatalf("thinking row was shown more than once: %q", output.String())
	}
	frontend.handleEvent(EvReasoningDelta{TurnID: "turn-1", Text: "private"})
	if !frontend.reasoningActive || strings.Contains(output.String(), "private") {
		t.Fatalf("reasoning delta changed state or leaked text: active=%t output=%q", frontend.reasoningActive, output.String())
	}
	frontend.handleEvent(EvReasoningCompleted{TurnID: "turn-1"})
	if frontend.reasoningActive {
		t.Fatal("reasoning stayed active after completion")
	}
	if !frontend.reasoningShown {
		t.Fatal("reasoningShown must stay set for the rest of the turn")
	}
}

// 原始 codex label 逐字傳遞，不被 normalized status 改寫。
func TestToolCompletionLabelIsShownVerbatim(t *testing.T) {
	frontend := &ui{screenMode: true}
	frontend.handleEvent(EvToolStarted{ToolID: "i1", Kind: ToolCommand, Title: "$ false", Command: "false"})
	frontend.handleEvent(EvToolCompleted{ToolID: "i1", Status: ToolFailed, Label: "failed exit 1"})
	frontend.showToolOutput = true
	if plain := stripANSI(frontend.screenFrame(100, 20)); !strings.Contains(plain, "failed exit 1") {
		t.Fatalf("label was rewritten: %q", plain)
	}
	frontend.handleEvent(EvTurnCompleted{Status: TurnFailed, Label: "failed"})
	if plain := stripANSI(frontend.screenFrame(100, 20)); !strings.Contains(plain, "turn failed") {
		t.Fatalf("turn label was rewritten: %q", plain)
	}
}

// scriptedRPCWriter 依 method 回覆 result 或 error，用來驗證 reconnect 的 resume -> new fallback。
type scriptedRPCWriter struct {
	output  *io.PipeWriter
	handler func(method string) (any, string)
}

func (w *scriptedRPCWriter) Write(value []byte) (int, error) {
	var request struct {
		ID     *int64 `json:"id"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(value), &request); err != nil {
		return 0, err
	}
	if request.ID == nil {
		return len(value), nil
	}
	result, failure := w.handler(request.Method)
	response := map[string]any{"id": *request.ID, "result": result}
	if failure != "" {
		response = map[string]any{"id": *request.ID, "error": map[string]any{"code": -32600, "message": failure}}
	}
	encoded, _ := json.Marshal(response)
	if _, err := fmt.Fprintf(w.output, "%s\n", encoded); err != nil {
		return 0, err
	}
	return len(value), nil
}

func (w *scriptedRPCWriter) Close() error { return w.output.Close() }

func scriptedCodexBackend(t *testing.T, handler func(method string) (any, string)) Backend {
	t.Helper()
	reader, writer := io.Pipe()
	client := newAppServer(&scriptedRPCWriter{output: writer, handler: handler}, reader, &exec.Cmd{})
	go client.readLoop()
	return newCodexBackend(client, "")
}

// T-P1-07：resume 找不到 rollout 且 transcript 沒內容時開新 session；有內容時保留錯誤。
func TestReconnectStartsFreshSessionOnlyWithoutTranscript(t *testing.T) {
	handler := func(method string) (any, string) {
		switch method {
		case "thread/resume":
			return nil, "no rollout found for thread id thread-1"
		case "thread/start":
			return map[string]any{"thread": map[string]any{"id": "fresh", "cwd": "/tmp/project"}}, ""
		default:
			return map[string]any{}, ""
		}
	}

	result := reconnectWithSnapshot(func() (Backend, error) { return scriptedCodexBackend(t, handler), nil },
		reconnectSnapshot{threadID: "thread-1", cwd: "/tmp/project"}, nil)
	if result.err != nil || result.thread.ID != "fresh" || result.backend == nil {
		t.Fatalf("fresh session result = %#v", result)
	}
	result.backend.Close()

	result = reconnectWithSnapshot(func() (Backend, error) { return scriptedCodexBackend(t, handler), nil },
		reconnectSnapshot{threadID: "thread-1", cwd: "/tmp/project", hasTranscriptContent: true}, nil)
	if result.err == nil || !strings.Contains(result.err.Error(), "no rollout found") {
		t.Fatalf("resume failure with transcript = %#v", result)
	}
}
