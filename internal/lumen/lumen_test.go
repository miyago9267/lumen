package lumen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/term"
)

func TestParseOptions(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantPrompt string
		wantResume string
		wantCont   bool
	}{
		{
			name:       "prompt and resume",
			args:       []string{"--resume", "thread-1", "fix", "the", "bug"},
			wantPrompt: "fix the bug",
			wantResume: "thread-1",
		},
		{
			name:       "continue",
			args:       []string{"--continue", "  inspect  "},
			wantPrompt: "inspect",
			wantCont:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseOptions(test.args)
			if err != nil {
				t.Fatalf("parseOptions() error = %v", err)
			}
			if got.prompt != test.wantPrompt || got.resume != test.wantResume || got.continue_ != test.wantCont {
				t.Fatalf("parseOptions() = %#v", got)
			}
		})
	}
}

func TestParseOptionsRejectsConflictingThreadModes(t *testing.T) {
	_, err := parseOptions([]string{"--resume", "thread-1", "--continue"})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("parseOptions() error = %v", err)
	}
}

func TestParseOptionsDefaultsToFullscreen(t *testing.T) {
	got, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if !got.fullscreen || got.minimal {
		t.Fatalf("default mode = fullscreen:%t minimal:%t", got.fullscreen, got.minimal)
	}

	for _, args := range [][]string{{"--minimal"}, {"--no-alt-screen"}, {"--fullscreen=false"}} {
		got, err := parseOptions(args)
		if err != nil {
			t.Fatalf("parseOptions(%v) error = %v", args, err)
		}
		if got.fullscreen {
			t.Fatalf("parseOptions(%v) kept fullscreen mode", args)
		}
	}
}

func TestRuntimeOverridesMapToAppServerFields(t *testing.T) {
	overrides := runtimeOverrides{
		model:           "gpt-test",
		reasoningEffort: "high",
		approvalPolicy:  "on-request",
		sandbox:         "read-only",
	}
	threadParams := map[string]any{}
	applyThreadOverrides(threadParams, overrides)
	if threadParams["model"] != "gpt-test" || threadParams["approvalPolicy"] != "on-request" || threadParams["sandbox"] != "read-only" {
		t.Fatalf("thread overrides = %#v", threadParams)
	}

	turnParams := map[string]any{}
	applyTurnOverrides(turnParams, overrides)
	if turnParams["model"] != "gpt-test" || turnParams["effort"] != "high" || turnParams["approvalPolicy"] != "on-request" {
		t.Fatalf("turn overrides = %#v", turnParams)
	}
	if _, ok := turnParams["sandboxPolicy"]; ok {
		t.Fatal("sandbox should be applied at thread scope")
	}
}

func TestParseOptionsRejectsUnsafeRuntimeValues(t *testing.T) {
	for _, args := range [][]string{
		{"--sandbox", "unknown"},
		{"--approval-policy", "always"},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("parseOptions(%v) accepted an invalid value", args)
		}
	}
}

func TestAppServerArgsSuppressUnstableFeatureBanner(t *testing.T) {
	args := appServerArgs()
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "app-server") || !strings.Contains(joined, "--listen stdio://") || !strings.Contains(joined, "suppress_unstable_features_warning=true") {
		t.Fatalf("app-server args = %v", args)
	}
}

func TestAppServerCallRoundTrip(t *testing.T) {
	stdoutReader, stdoutWriter := io.Pipe()
	stdin := &respondingWriter{output: stdoutWriter}
	client := newAppServer(stdin, stdoutReader, &exec.Cmd{})
	go client.readLoop()

	var result struct {
		Value string
	}
	if err := client.Call("test/method", map[string]string{"input": "ok"}, &result); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if result.Value != "ok" {
		t.Fatalf("Call() result = %#v", result)
	}

	client.Close()
}

func TestAppServerReadLoopClosesAfterErrorQueueIsFull(t *testing.T) {
	stdoutReader, stdoutWriter := io.Pipe()
	client := newAppServer(&bufferWriteCloser{Buffer: &bytes.Buffer{}}, stdoutReader, &exec.Cmd{})
	client.errors <- errors.New("previous error")
	done := make(chan struct{})
	go func() {
		client.readLoop()
		close(done)
	}()
	_ = stdoutWriter.CloseWithError(errors.New("fixture decode failure"))

	select {
	case <-client.closed:
	case <-time.After(time.Second):
		t.Fatal("readLoop did not close the client after a decode error")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("readLoop remained blocked after a decode error")
	}
	client.Close()
}

func TestRespondWritesJSONRPCResponse(t *testing.T) {
	var output bytes.Buffer
	client := newAppServer(&bufferWriteCloser{Buffer: &output}, io.NopCloser(strings.NewReader("")), &exec.Cmd{})
	if err := client.respond(json.RawMessage("7"), map[string]string{"decision": "accept"}); err != nil {
		t.Fatalf("respond() error = %v", err)
	}

	var message struct {
		ID     int
		Result map[string]string
	}
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &message); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if message.ID != 7 || message.Result["decision"] != "accept" {
		t.Fatalf("response = %#v", message)
	}
}

func TestInputEditing(t *testing.T) {
	value := []rune("ab\nxyz")
	if got := lineStart(value, 2); got != 0 {
		t.Fatalf("lineStart() = %d, want 0", got)
	}
	if got := lineEnd(value, 1); got != 2 {
		t.Fatalf("lineEnd() = %d, want 2", got)
	}

	ui := &ui{input: value, cursor: 1}
	ui.moveVertical(1)
	if ui.cursor != 4 {
		t.Fatalf("moveVertical(down) cursor = %d, want 4", ui.cursor)
	}
	ui.moveVertical(-1)
	if ui.cursor != 1 {
		t.Fatalf("moveVertical(up) cursor = %d, want 1", ui.cursor)
	}
}

func TestPromptHistoryNavigation(t *testing.T) {
	var output bytes.Buffer
	frontend := &ui{
		input:        []rune("draft"),
		cursor:       len([]rune("draft")),
		out:          &output,
		historyIndex: -1,
	}
	frontend.rememberPrompt("first")
	frontend.rememberPrompt("second")

	if err := frontend.handleKey(keyEvent{typ: keyUp}); err != nil {
		t.Fatalf("history up error = %v", err)
	}
	if frontend.cursor != 0 {
		t.Fatalf("first up cursor = %d, want prompt start", frontend.cursor)
	}
	if err := frontend.handleKey(keyEvent{typ: keyUp}); err != nil {
		t.Fatalf("second history up error = %v", err)
	}
	if got := string(frontend.input); got != "second" {
		t.Fatalf("second up history = %q, want second", got)
	}
	if err := frontend.handleKey(keyEvent{typ: keyDown}); err != nil {
		t.Fatalf("history down error = %v", err)
	}
	if got := string(frontend.input); got != "draft" {
		t.Fatalf("history down restore = %q, want draft", got)
	}
}

func TestPromptBoundaryNavigationPrecedesHistory(t *testing.T) {
	frontend := &ui{
		input:        []rune("first\nsecond"),
		cursor:       3,
		inputHistory: []string{"previous"},
		historyIndex: -1,
	}
	frontend.movePromptUp()
	if frontend.cursor != 0 {
		t.Fatalf("up from first line cursor = %d, want prompt start", frontend.cursor)
	}
	frontend.movePromptUp()
	if got := string(frontend.input); got != "previous" {
		t.Fatalf("second up input = %q, want history entry", got)
	}

	frontend = &ui{
		input:        []rune("first\nsecond"),
		cursor:       len([]rune("first\nsecond")) - 1,
		inputHistory: []string{"next"},
		historyIndex: 0,
		savedInput:   []rune("draft"),
	}
	frontend.movePromptDown()
	if frontend.cursor != len(frontend.input) {
		t.Fatalf("down from last line cursor = %d, want prompt end", frontend.cursor)
	}
	frontend.movePromptDown()
	if got := string(frontend.input); got != "draft" {
		t.Fatalf("second down input = %q, want restored draft", got)
	}
}

func TestMouseEditingEvents(t *testing.T) {
	input := strings.NewReader("\x1b[<0;8;14M\x1b[<35;8;14M\x1b[<0;8;14m")
	keys := make(chan keyEvent, 3)
	go readKeys(input, keys)

	var got []keyEvent
	for key := range keys {
		got = append(got, key)
	}
	if len(got) != 3 || got[0].typ != keyMouseClick || got[0].mouseX != 8 || got[0].mouseY != 14 || got[1].typ != keyMouseMove || got[2].typ != keyMouseRelease {
		t.Fatalf("mouse events = %#v", got)
	}
}

func TestMouseClickPlacesPromptCursor(t *testing.T) {
	frontend := &ui{screenMode: true, input: []rune("hello"), cursor: 5}
	if !frontend.placeCursorFromMouse(8, 14, 80, 16) {
		t.Fatal("mouse click outside composer")
	}
	if frontend.cursor != 3 {
		t.Fatalf("mouse cursor = %d, want 3", frontend.cursor)
	}
}

func TestReadKeysParsesDashboardChord(t *testing.T) {
	keys := make(chan keyEvent, 1)
	go readKeys(strings.NewReader("\x1c"), keys)
	got, ok := <-keys
	if !ok || got.typ != keyCtrlBackslash {
		t.Fatalf("dashboard chord = %#v, want Ctrl+\\", got)
	}
}

func TestReadKeysParsesCtrlEnter(t *testing.T) {
	keys := make(chan keyEvent, 2)
	go readKeys(strings.NewReader("\x1b[13;5u\x1b[27;5;13~"), keys)
	for index := 0; index < 2; index++ {
		key, ok := <-keys
		if !ok || key.typ != keyCtrlEnter {
			t.Fatalf("Ctrl+Enter event %d = %#v", index, key)
		}
	}
}

func TestReadKeysParsesShiftEnter(t *testing.T) {
	keys := make(chan keyEvent, 2)
	go readKeys(strings.NewReader("\x1b[13;2u\x1b[27;2;13~"), keys)
	for index := 0; index < 2; index++ {
		key, ok := <-keys
		if !ok || key.typ != keyShiftEnter {
			t.Fatalf("Shift+Enter event %d = %#v", index, key)
		}
	}
}

func TestBusyPromptQueuesAndCancelAndSendIsDistinct(t *testing.T) {
	frontend := &ui{screenMode: true, busy: true, thread: threadSummary{ID: "thread-1"}}
	frontend.input = []rune("queue this")
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatalf("queue prompt error = %v", err)
	}
	if len(frontend.queuedPrompts) != 1 || frontend.queuedPrompts[0] != "queue this" {
		t.Fatalf("queued prompts = %#v", frontend.queuedPrompts)
	}
	frontend.input = []rune("take over")
	frontend.turnID = "turn-1"
	if err := frontend.handleKey(keyEvent{typ: keyCtrlEnter}); err == nil || !strings.Contains(err.Error(), "app-server") {
		t.Fatalf("cancel-and-send error = %v", err)
	}
	if frontend.cancelAndSend != "take over" || len(frontend.queuedPrompts) != 1 {
		t.Fatalf("cancel-and-send state = pending:%q queue:%#v", frontend.cancelAndSend, frontend.queuedPrompts)
	}
}

func TestDashboardShowsSessionPeekAndSelection(t *testing.T) {
	frontend := &ui{
		screenMode: true,
		cwd:        "/tmp/project",
		thread:     threadSummary{ID: "thread-current", Name: "active"},
		dashboard:  true,
		dashboardRows: []threadSummary{
			{ID: "thread-current", Name: "active", Status: "working", Preview: "current request"},
			{ID: "thread-other", Name: "investigation", Status: "completed", Preview: "last answer from another session"},
		},
	}
	plain := stripANSI(frontend.screenFrame(100, 24))
	for _, expected := range []string{"dashboard", "session supervisor", "current request", "last answer from another session", "1", "2"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("dashboard missing %q: %q", expected, plain)
		}
	}
	if err := frontend.handleDashboardKey(keyEvent{typ: keyDown}); err != nil {
		t.Fatalf("dashboard selection error = %v", err)
	}
	if frontend.dashboardIndex != 1 {
		t.Fatalf("dashboard index = %d, want 1", frontend.dashboardIndex)
	}
}

func TestCtrlUOpensResumePicker(t *testing.T) {
	frontend := &ui{
		screenMode: true,
		cwd:        "/tmp/project",
		thread:     threadSummary{ID: "thread-current"},
	}
	if err := frontend.handleKey(keyEvent{typ: keyCtrlU}); err != nil {
		t.Fatalf("Ctrl+U error = %v", err)
	}
	if !frontend.dashboard {
		t.Fatal("Ctrl+U did not open the resume picker")
	}
}

func TestPendingRequestTakesPriorityOverDashboard(t *testing.T) {
	frontend := &ui{
		screenMode: true,
		dashboard:  true,
		approval:   &approvalRequest{available: []string{"accept", "decline"}},
	}
	if err := frontend.handleKey(keyEvent{typ: keyCtrlBackslash}); err != nil {
		t.Fatalf("dashboard chord with pending approval = %v", err)
	}
	if !frontend.dashboard {
		t.Fatal("dashboard should not toggle while approval is pending")
	}

	frontend.showApproval(serverRequest{}, "run command", "/tmp/project", "reason", []string{"accept"})
	if frontend.dashboard {
		t.Fatal("approval should close dashboard")
	}
}

func TestThreadPreviewUsesLatestAssistantMessage(t *testing.T) {
	thread := threadSummary{
		Turns: []historyTurn{{Items: []historyItem{
			{Type: "userMessage", Content: []historyInput{{Type: "text", Text: "question"}}},
			{Type: "agentMessage", Text: "answer\nwith context"},
		}}},
	}
	if got := threadPreview(thread); got != "answer with context" {
		t.Fatalf("threadPreview() = %q", got)
	}
}

func TestTerminalOutputSanitization(t *testing.T) {
	got := sanitizeText("ok\x1b[31mred\x1b[0m\rnext\x00")
	if got != "ok[31mred[0m\nnext" {
		t.Fatalf("sanitizeText() = %q", got)
	}
	if got := indentText("one\ntwo", "  "); got != "  one\n  two" {
		t.Fatalf("indentText() = %q", got)
	}
}

func TestSandboxLabel(t *testing.T) {
	if got := sandboxLabel(json.RawMessage("{\"type\":\"workspaceWrite\"}")); got != "workspace-write" {
		t.Fatalf("sandboxLabel(object) = %q", got)
	}
	if got := sandboxLabel(json.RawMessage("\"read-only\"")); got != "read-only" {
		t.Fatalf("sandboxLabel(string) = %q", got)
	}
	if got := sandboxLabel(nil); got != "inherit" {
		t.Fatalf("sandboxLabel(empty) = %q", got)
	}
}

func TestRenderHistory(t *testing.T) {
	var output bytes.Buffer
	frontend := &ui{
		thread: threadSummary{
			Turns: []historyTurn{{
				Items: []historyItem{
					{Type: "userMessage", Content: []historyInput{{Type: "text", Text: "previous question"}}},
					{Type: "agentMessage", Text: "previous answer"},
					{Type: "commandExecution", Command: "pwd", AggregatedOutput: "/tmp/project\n", Status: "completed", ExitCode: float64(0)},
				},
			}},
		},
		out: &output,
	}
	frontend.renderHistory()
	for _, expected := range []string{"recent history", "previous question", "previous answer", "$ pwd", "/tmp/project"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("history output missing %q: %q", expected, output.String())
		}
	}
	if strings.Contains(output.String(), "exit 0") || strings.Contains(output.String(), "completed") {
		t.Fatalf("successful tool completion should stay quiet: %q", output.String())
	}
}

func TestCompactWarning(t *testing.T) {
	message := "Under-development features enabled: chronicle, default_mode_request_user_input. Under-development features are incomplete and may behave unpredictably. To suppress this warning, set config."
	if got := compactWarning(message); got != "Codex warning: unstable features active (chronicle, default_mode_request_user_input)" {
		t.Fatalf("compactWarning() = %q", got)
	}
	if got := compactWarning("ordinary warning"); got != "ordinary warning" {
		t.Fatalf("compactWarning(ordinary) = %q", got)
	}
}

func TestScreenFrameHasAppShellLayout(t *testing.T) {
	frontend := &ui{
		screenMode: true,
		branch:     "feat/lumen-tui",
		cwd:        "/tmp/project",
		thread: threadSummary{
			Model:           "gpt-test",
			ReasoningEffort: "high",
			ApprovalPolicy:  "on-request",
		},
		input:  []rune("hi"),
		cursor: 2,
	}
	frontend.screenAdd(screenEvent, "session_start  [app-server]")
	frontend.screenAdd(screenAssistant, "ready")
	plain := stripANSI(frontend.screenFrame(80, 16))

	for _, expected := range []string{
		"feat/lumen-tui",
		"/tmp/project",
		"gpt-test · high",
		"session_start  [app-server]",
		"ready",
		"┌─ prompt",
		"hi",
		"Enter send",
	} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("screen frame missing %q: %q", expected, plain)
		}
	}
	if lines := strings.Split(plain, "\r\n"); len(lines) != 16 {
		t.Fatalf("screen frame lines = %d, want 16", len(lines))
	}
	frameLines := strings.Split(plain, "\r\n")
	sessionLine, composerLine := -1, -1
	for index, line := range frameLines {
		if strings.Contains(line, "session_start") {
			sessionLine = index
		}
		if strings.HasPrefix(line, "┌─ prompt") {
			composerLine = index
		}
	}
	if sessionLine < 0 || composerLine < 0 || sessionLine >= composerLine || sessionLine > 4 {
		t.Fatalf("transcript is not top-aligned: session=%d composer=%d", sessionLine, composerLine)
	}
}

func TestScreenFrameLeavesGapBeforeComposer(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	for index := 0; index < 12; index++ {
		frontend.screenAdd(screenAssistant, fmt.Sprintf("answer-%02d", index))
	}

	frameLines := strings.Split(stripANSI(frontend.screenFrame(80, 16)), "\r\n")
	composerLine := -1
	for index, line := range frameLines {
		if strings.HasPrefix(line, "┌─ prompt") {
			composerLine = index
			break
		}
	}
	if composerLine < 2 || frameLines[composerLine-1] != "" {
		t.Fatalf("composer has no separating gap: line=%d frame=%q", composerLine, frameLines)
	}
	if !strings.Contains(frameLines[composerLine-2], "answer-11") {
		t.Fatalf("latest message block is not immediately before gap: line=%d frame=%q", composerLine, frameLines)
	}
	if len(frameLines) != 16 {
		t.Fatalf("screen frame lines = %d, want 16", len(frameLines))
	}
}

func TestScreenDockSitsBelowComposer(t *testing.T) {
	frontend := &ui{
		screenMode: true,
		cwd:        "/tmp/project",
		thread:     threadSummary{Model: "gpt-test", ReasoningEffort: "medium"},
		input:      []rune("hello"),
		cursor:     5,
	}
	frontend.screenAdd(screenAssistant, "answer")

	lines := strings.Split(stripANSI(frontend.screenFrame(100, 16)), "\r\n")
	composerBottom := -1
	for index, line := range lines {
		if strings.HasPrefix(line, "└─") {
			composerBottom = index
		}
	}
	if composerBottom < 0 || composerBottom+1 >= len(lines) {
		t.Fatalf("composer bottom or dock missing: %q", lines)
	}
	if strings.Contains(lines[composerBottom], "carolline") {
		t.Fatalf("carolline status is still embedded in composer border: %q", lines[composerBottom])
	}
	if !strings.Contains(lines[composerBottom+1], "carolline") || !strings.Contains(lines[composerBottom+1], "Enter send") {
		t.Fatalf("dock does not combine status and controls: %q", lines[composerBottom+1])
	}
}

func TestCarollineDockProjectsAttentionWithoutTranscriptDuplication(t *testing.T) {
	frontend := &ui{
		screenMode: true,
		cwd:        "/tmp/project",
		thread: threadSummary{
			Name:            "release thread",
			Model:           "gpt-test",
			ReasoningEffort: "medium",
			ApprovalPolicy:  "on-request",
			Sandbox:         json.RawMessage(`{"type":"readOnly"}`),
		},
		client: &appServer{},
	}
	frontend.screenAdd(screenAssistant, "assistant answer")
	lines := strings.Split(stripANSI(frontend.screenFrame(120, 18)), "\r\n")
	dock := lines[len(lines)-1]
	if !strings.Contains(dock, "carolline") || strings.Contains(dock, "assistant answer") {
		t.Fatalf("idle dock = %q", dock)
	}
	header := stripANSI(frontend.screenHeaderLine(120))
	for _, expected := range []string{"release thread", "on-request", "read-only"} {
		if !strings.Contains(header, expected) {
			t.Fatalf("header metadata missing %q: %q", expected, header)
		}
	}

	frontend.busy = true
	frontend.queuedPrompts = []string{"next prompt"}
	dock = stripANSI(frontend.screenDockLine(120))
	if !strings.Contains(dock, "working") || !strings.Contains(dock, "queued") {
		t.Fatalf("working dock = %q", dock)
	}

	frontend.busy = false
	frontend.approval = &approvalRequest{available: []string{"accept", "decline"}}
	dock = stripANSI(frontend.screenDockLine(120))
	if !strings.Contains(dock, "needs input") || !strings.Contains(dock, "[a]ccept") {
		t.Fatalf("approval dock = %q", dock)
	}

	frontend.approval = nil
	frontend.markReconnectFailed(errors.New("fixture reconnect"), 2)
	dock = stripANSI(frontend.screenDockLine(120))
	if !strings.Contains(dock, "reconnecting") || !strings.Contains(dock, "attempt 2") || !strings.Contains(dock, "retry pending") {
		t.Fatalf("reconnect dock = %q", dock)
	}

	frontend.connectionError = ""
	frontend.client = nil
	dock = stripANSI(frontend.screenDockLine(120))
	if !strings.Contains(dock, "detached") || !strings.Contains(dock, "app-server unavailable") {
		t.Fatalf("detached dock = %q", dock)
	}
}

func TestFreshFullscreenThreadShowsCompactWelcome(t *testing.T) {
	frontend := newUI(nil, threadSummary{Model: "gpt-test", ReasoningEffort: "medium"}, "/tmp/project", true, runtimeOverrides{})
	frontend.screenMode = true
	plain := stripANSI(frontend.screenFrame(80, 16))

	for _, expected := range []string{
		"Lumen",
		"ready for a new thread",
		"try a prompt",
		"inspect the current repo",
		"Ctrl+X shortcuts",
	} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("welcome frame missing %q: %q", expected, plain)
		}
	}
	if strings.Contains(plain, "session_start") {
		t.Fatalf("welcome frame leaked startup event: %q", plain)
	}

	frontend.showWelcome = false
	frontend.screenAdd(screenAssistant, "first response")
	plain = stripANSI(frontend.screenFrame(80, 16))
	if strings.Contains(plain, "ready for a new thread") || !strings.Contains(plain, "first response") {
		t.Fatalf("welcome did not yield to transcript: %q", plain)
	}
}

func TestScreenHeaderShowsContextUsage(t *testing.T) {
	frontend := &ui{
		screenMode: true,
		branch:     "feat/lumen-tui",
		cwd:        "/tmp/project",
		thread:     threadSummary{ID: "thread-1", Model: "gpt-test", ReasoningEffort: "high"},
	}
	frontend.handleNotification(rpcMessage{
		Method: "thread/tokenUsage/updated",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","tokenUsage":{"total":{"totalTokens":7300},"last":{"totalTokens":7300},"modelContextWindow":500000}}`),
	})
	plain := stripANSI(frontend.screenHeaderLine(120))
	if !strings.Contains(plain, "7.3K / 500K") {
		t.Fatalf("screen header missing context usage: %q", plain)
	}
}

func TestScreenWrappingRespectsDisplayWidth(t *testing.T) {
	if got := displayWidth("abc"); got != 3 {
		t.Fatalf("ASCII display width = %d, want 3", got)
	}
	if got := displayWidth("你好"); got != 4 {
		t.Fatalf("CJK display width = %d, want 4", got)
	}
	for _, line := range wrapDisplay("你好 codex", 8) {
		if got := displayWidth(line); got > 8 {
			t.Fatalf("wrapped line %q has display width %d", line, got)
		}
	}
}

func TestComposerCursorDoesNotConsumeInputCell(t *testing.T) {
	frontend := &ui{
		screenMode: true,
		input:      []rune("abcdefghijklmn"),
		cursor:     len([]rune("abcdefghijklmn")),
	}
	lines := frontend.screenComposerLines(20)
	if len(lines) != 3 {
		t.Fatalf("composer lines = %d, want one input row plus borders: %v", len(lines), lines)
	}
	plain := stripANSI(strings.Join(lines, "\n"))
	if strings.Contains(plain, "█") {
		t.Fatalf("composer rendered a width-consuming cursor: %q", plain)
	}
	if !strings.Contains(plain, "abcdefghijklmn") {
		t.Fatalf("composer lost input text: %q", plain)
	}
	row, column, ok := frontend.screenCursorPosition(20, 8)
	if !ok || row != 6 || column != 19 {
		t.Fatalf("screen cursor position = (%d, %d, %t), want (6, 19, true)", row, column, ok)
	}
}

func TestScreenInputAndOutputShareTurnCardWithSeparateColors(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	frontend.screenAdd(screenUser, "hello")
	frontend.screenAdd(screenAssistant, "world")
	rows := frontend.screenTranscriptRows(80)
	if len(rows) < 5 {
		t.Fatalf("transcript rows = %d, want a turn card: %#v", len(rows), rows)
	}
	userRow, assistantRow := screenTranscriptRow{}, screenTranscriptRow{}
	for _, row := range rows {
		if row.copyText == "hello" {
			userRow = row
		}
		if row.copyText == "world" {
			assistantRow = row
		}
	}
	plain := stripANSI(strings.Join(func() []string {
		values := make([]string, 0, len(rows))
		for _, row := range rows {
			values = append(values, row.rendered)
		}
		return values
	}(), ""))
	if !strings.Contains(plain, "› hello") || !strings.Contains(plain, "world") {
		t.Fatalf("turn card content missing: %q", plain)
	}
	if !strings.Contains(plain, "┌─ turn 1") || !strings.Contains(plain, "└") || !strings.Contains(plain, "[ask]") {
		t.Fatalf("turn card chrome missing: %q", plain)
	}
	if userRow.turnKey == "" || userRow.turnKey != assistantRow.turnKey {
		t.Fatalf("turn grouping = (%q, %q), want the same turn key", userRow.turnKey, assistantRow.turnKey)
	}
	if userStyle, assistantStyle := screenStyle(screenUser), screenStyle(screenAssistant); userStyle != "" || assistantStyle != "" {
		if userStyle == assistantStyle || !strings.Contains(userRow.rendered, userStyle) || !strings.Contains(assistantRow.rendered, assistantStyle) {
			t.Fatalf("input/output colors = (%q, %q), rows = %#v", userStyle, assistantStyle, rows)
		}
	}
}

func TestScreenTurnUsesFlatRoleRowsWithHoverDetail(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project", showToolOutput: true}
	frontend.screenAdd(screenUser, "hello")
	frontend.screenAdd(screenAssistant, "world")
	frontend.screenAdd(screenTool, "$ pwd")
	frontend.screenAdd(screenStatus, "completed")

	plain := stripANSI(frontend.screenFrame(100, 30))
	for _, expected := range []string{"you › hello", "assistant › world", "tool ↳ $ pwd", "status · completed"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("flat role row missing %q: %q", expected, plain)
		}
	}
	for _, label := range []string{"┌─ you ", "┌─ assistant ", "┌─ tool ", "┌─ status "} {
		if strings.Contains(plain, label) {
			t.Fatalf("default transcript unexpectedly boxed %q: %q", label, plain)
		}
	}
	rows := frontend.screenTranscriptRows(100)
	assistantIndex := -1
	for index, row := range rows {
		if row.copyText == "world" {
			assistantIndex = index
			break
		}
	}
	if assistantIndex < 0 {
		t.Fatalf("assistant row missing: %#v", rows)
	}
	frontend.updateHover(rows[assistantIndex].copyStart+1, assistantIndex+3, 100, 30)
	if !strings.Contains(frontend.hoverText, "assistant") || !strings.Contains(frontend.hoverText, "world") {
		t.Fatalf("hover detail = %q", frontend.hoverText)
	}
	if !strings.Contains(frontend.screenFrame(100, 30), "\x1b[7m") {
		t.Fatal("hovered role row was not visually highlighted")
	}
	if userStyle, assistantStyle := screenStyle(screenUser), screenStyle(screenAssistant); userStyle != "" || assistantStyle != "" {
		var userRow, assistantRow screenTranscriptRow
		for _, row := range rows {
			if row.copyText == "hello" {
				userRow = row
			}
			if row.copyText == "world" {
				assistantRow = row
			}
		}
		if userStyle == assistantStyle || !strings.Contains(userRow.rendered, userStyle) || !strings.Contains(assistantRow.rendered, assistantStyle) {
			t.Fatalf("role block colors = (%q, %q), rows = %#v", userStyle, assistantStyle, rows)
		}
	}
}

func TestUIRecoveryContainsHandlerPanic(t *testing.T) {
	frontend := &ui{screenMode: true}
	err := frontend.recoverUI("test event", func() {
		panic("fixture panic")
	})
	if err == nil || !strings.Contains(err.Error(), "test event") || !strings.Contains(err.Error(), "fixture panic") {
		t.Fatalf("recoverUI() error = %v", err)
	}
}

func TestConnectionLossKeepsFullscreenState(t *testing.T) {
	frontend := &ui{
		screenMode: true,
		thread:     threadSummary{ID: "thread-1"},
		busy:       true,
		turnID:     "turn-1",
	}
	frontend.beginScreenTurn("turn-1")
	frontend.screenAdd(screenUser, "keep this prompt")
	frontend.markConnectionLost(errors.New("read app-server: fixture failure"))

	if frontend.busy || frontend.turnID != "" {
		t.Fatalf("connection loss left active turn: busy:%t turn:%q", frontend.busy, frontend.turnID)
	}
	plain := stripANSI(frontend.screenFrame(100, 24))
	for _, expected := range []string{"connection lost", "reconnecting", "keep this prompt"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("connection-loss screen missing %q: %q", expected, plain)
		}
	}
}

func TestTerminalLifecycleRestoresOnlyOnce(t *testing.T) {
	var output bytes.Buffer
	makeRawCalls := 0
	restoreCalls := 0
	lifecycle := newTerminalLifecycle(7, &output, "enter", "exit")
	lifecycle.makeRaw = func(fd int) (*term.State, error) {
		if fd != 7 {
			t.Fatalf("raw terminal fd = %d, want 7", fd)
		}
		makeRawCalls++
		return &term.State{}, nil
	}
	lifecycle.restore = func(fd int, state *term.State) error {
		restoreCalls++
		return nil
	}

	if err := lifecycle.Enter(); err != nil {
		t.Fatalf("Enter() error = %v", err)
	}
	if err := lifecycle.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := lifecycle.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if output.String() != "enterexit" {
		t.Fatalf("terminal sequences = %q, want enterexit", output.String())
	}
	if makeRawCalls != 1 || restoreCalls != 1 {
		t.Fatalf("lifecycle calls = makeRaw:%d restore:%d, want 1/1", makeRawCalls, restoreCalls)
	}
}

func TestTerminalLifecycleSetupFailureRestoresRawMode(t *testing.T) {
	var output failingWriter
	restoreCalls := 0
	lifecycle := newTerminalLifecycle(7, &output, "enter", "exit")
	lifecycle.makeRaw = func(int) (*term.State, error) { return &term.State{}, nil }
	lifecycle.restore = func(int, *term.State) error {
		restoreCalls++
		return nil
	}

	if err := lifecycle.Enter(); err == nil {
		t.Fatal("Enter() succeeded after terminal setup write failure")
	}
	if restoreCalls != 1 {
		t.Fatalf("setup failure restore calls = %d, want 1", restoreCalls)
	}
	if err := lifecycle.Close(); err != nil {
		t.Fatalf("Close() after failed Enter() error = %v", err)
	}
}

func TestFreshThreadResumeFallbackRequiresEmptyTranscript(t *testing.T) {
	missingRollout := errors.New("thread/resume (-32600): no rollout found for thread id thread-1")
	if !canStartFreshThreadAfterResume(missingRollout, false) {
		t.Fatal("empty thread should fall back to a fresh thread")
	}
	if canStartFreshThreadAfterResume(missingRollout, true) {
		t.Fatal("thread with transcript content must keep retrying resume")
	}
	if canStartFreshThreadAfterResume(errors.New("thread/resume failed"), false) {
		t.Fatal("unrelated resume errors must not create a new thread")
	}
}

func TestReconnectDelayIsBounded(t *testing.T) {
	if got := reconnectDelay(0); got != 250*time.Millisecond {
		t.Fatalf("initial reconnect delay = %s", got)
	}
	if got := reconnectDelay(1); got != 500*time.Millisecond {
		t.Fatalf("second reconnect delay = %s", got)
	}
	if got := reconnectDelay(99); got != 4*time.Second {
		t.Fatalf("bounded reconnect delay = %s", got)
	}
}

func TestSessionSupervisorPreventsDuplicateReconnects(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls int
	var callsMu sync.Mutex
	supervisor := newSessionSupervisor(func() (*appServer, error) {
		callsMu.Lock()
		calls++
		callsMu.Unlock()
		close(started)
		<-release
		return nil, errors.New("fixture reconnect failure")
	})
	defer supervisor.Close()

	if !supervisor.Start(reconnectSnapshot{}) {
		t.Fatal("first reconnect was not started")
	}
	<-started
	if supervisor.Start(reconnectSnapshot{}) {
		t.Fatal("duplicate reconnect was started while the first was active")
	}
	close(release)

	select {
	case result := <-supervisor.Results():
		if result.err == nil || !strings.Contains(result.err.Error(), "fixture reconnect failure") {
			t.Fatalf("reconnect result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnect result did not arrive")
	}
	callsMu.Lock()
	defer callsMu.Unlock()
	if calls != 1 {
		t.Fatalf("reconnect factory calls = %d, want 1", calls)
	}
}

func TestReconnectCancellationDoesNotStartFactory(t *testing.T) {
	done := make(chan struct{})
	close(done)
	called := false
	result := reconnectWithSnapshot(func() (*appServer, error) {
		called = true
		return nil, nil
	}, reconnectSnapshot{}, done)

	if !errors.Is(result.err, errReconnectCancelled) {
		t.Fatalf("cancelled reconnect error = %v", result.err)
	}
	if called {
		t.Fatal("cancelled reconnect invoked its factory")
	}
}

func TestReconnectCancellationClosesLateFactoryClient(t *testing.T) {
	done := make(chan struct{})
	release := make(chan struct{})
	started := make(chan struct{})
	client := newAppServer(&bufferWriteCloser{Buffer: &bytes.Buffer{}}, io.NopCloser(strings.NewReader("")), &exec.Cmd{})
	factory := func() (*appServer, error) {
		close(started)
		<-release
		return client, nil
	}
	resultCh := make(chan reconnectResult, 1)
	go func() {
		resultCh <- reconnectWithSnapshot(factory, reconnectSnapshot{}, done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("reconnect factory did not start")
	}
	close(done)
	close(release)
	select {
	case result := <-resultCh:
		if !errors.Is(result.err, errReconnectCancelled) {
			t.Fatalf("cancelled reconnect result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled reconnect did not return")
	}
	select {
	case <-client.closed:
	case <-time.After(time.Second):
		t.Fatal("late factory client was not closed")
	}
}

func TestReconnectFactoryPanicBecomesError(t *testing.T) {
	result := reconnectWithSnapshot(func() (*appServer, error) {
		panic("fixture factory panic")
	}, reconnectSnapshot{}, nil)
	if result.err == nil || !strings.Contains(result.err.Error(), "fixture factory panic") {
		t.Fatalf("factory panic result = %#v", result)
	}
}

func TestReconnectAsyncResumesCurrentThread(t *testing.T) {
	client := nativeRPCClient(t, func(method string, _ json.RawMessage) any {
		switch method {
		case "thread/resume":
			return map[string]any{
				"thread": map[string]any{"id": "thread-1", "cwd": "/tmp/project"},
				"model":  "gpt-test",
			}
		default:
			return map[string]any{}
		}
	})
	frontend := &ui{
		client:        nil,
		thread:        threadSummary{ID: "thread-1"},
		cwd:           "/tmp/project",
		serverFactory: func() (*appServer, error) { return client, nil },
	}
	done := make(chan struct{})
	resultCh := frontend.reconnectAsync(done)
	select {
	case result := <-resultCh:
		if result.err != nil || result.client != client || result.thread.ID != "thread-1" {
			t.Fatalf("reconnect result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnect did not complete")
	}
	close(done)
}

func TestScreenTurnCardExposesAskAndForkActions(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	frontend.beginScreenTurn("turn-1")
	frontend.screenAdd(screenUser, "question")
	frontend.screenAdd(screenAssistant, "answer")
	frontend.screenAdd(screenStatus, "turn completed")
	frontend.endScreenTurn()

	rows := frontend.screenTranscriptRows(80)
	plain := stripANSI(func() string {
		values := make([]string, 0, len(rows))
		for _, row := range rows {
			values = append(values, row.rendered)
		}
		return strings.Join(values, "\n")
	}())
	for _, expected := range []string{"┌─ turn 1", "› question", "answer", "turn completed", "[ask]", "[fork]", "└"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("turn card missing %q: %q", expected, plain)
		}
	}
	var ask, fork bool
	for _, row := range rows {
		for _, action := range row.actions {
			ask = ask || action.action == screenTurnActionAsk
			fork = fork || action.action == screenTurnActionFork
		}
	}
	if !ask || !fork {
		t.Fatalf("turn actions = ask:%t fork:%t rows:%#v", ask, fork, rows)
	}
}

func TestClickingTurnContentTargetsFollowUp(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	frontend.beginScreenTurn("turn-1")
	frontend.screenAdd(screenUser, "question")
	frontend.screenAdd(screenAssistant, "answer")
	frontend.endScreenTurn()

	rows := frontend.screenTranscriptRows(80)
	contentIndex := -1
	for index, row := range rows {
		if row.copyText == "answer" {
			contentIndex = index
			break
		}
	}
	if contentIndex < 0 {
		t.Fatalf("answer row missing: %#v", rows)
	}
	x, y := rows[contentIndex].copyStart+1, contentIndex+3
	if !frontend.beginScreenSelection(x, y, 80, 24) {
		t.Fatal("turn click did not start")
	}
	if err := frontend.finishScreenSelection(x, y, 80, 24); err != nil {
		t.Fatalf("turn click error = %v", err)
	}
	if frontend.replyTarget.key == "" || frontend.replyTarget.id != "turn-1" || frontend.replyTarget.ordinal != 1 {
		t.Fatalf("reply target = %#v", frontend.replyTarget)
	}
	if plain := stripANSI(strings.Join(frontend.screenComposerLines(80), "\n")); !strings.Contains(plain, "ask turn 1") {
		t.Fatalf("follow-up composer label missing: %q", plain)
	}
}

func TestClickingTurnForkUsesTurnBoundary(t *testing.T) {
	var gotMethod string
	var gotParams map[string]any
	client := nativeRPCClient(t, func(method string, raw json.RawMessage) any {
		gotMethod = method
		_ = json.Unmarshal(raw, &gotParams)
		return map[string]any{
			"thread":          map[string]any{"id": "forked-thread"},
			"model":           "gpt-test",
			"cwd":             "/tmp/project",
			"approvalPolicy":  "on-request",
			"sandbox":         "read-only",
			"reasoningEffort": "medium",
		}
	})
	frontend := &ui{
		client:     client,
		screenMode: true,
		cwd:        "/tmp/project",
		thread: threadSummary{
			ID: "thread-1",
			Turns: []historyTurn{{
				ID: "turn-1",
				Items: []historyItem{
					{Type: "userMessage", Content: []historyInput{{Type: "text", Text: "question"}}},
					{Type: "agentMessage", Text: "answer"},
				},
			}}},
	}
	frontend.renderHistory()
	rows := frontend.screenTranscriptRows(80)
	actionIndex, fork := -1, screenActionRange{}
	for index, row := range rows {
		for _, action := range row.actions {
			if action.action == screenTurnActionFork {
				actionIndex, fork = index, action
			}
		}
	}
	if actionIndex < 0 {
		t.Fatalf("fork action missing: %#v", rows)
	}
	x, y := fork.start+1, actionIndex+3
	if !frontend.beginScreenSelection(x, y, 80, 24) {
		t.Fatal("fork click did not start")
	}
	if err := frontend.finishScreenSelection(x, y, 80, 24); err != nil {
		t.Fatalf("fork click error = %v", err)
	}
	if gotMethod != "thread/fork" || gotParams["threadId"] != "thread-1" || gotParams["lastTurnId"] != "turn-1" {
		t.Fatalf("fork request = method:%q params:%#v", gotMethod, gotParams)
	}
	if frontend.thread.ID != "forked-thread" {
		t.Fatalf("adopted fork thread = %q", frontend.thread.ID)
	}
}

func TestScreenHoverShowsBlockDetail(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	frontend.screenAdd(screenEvent, "hook user_prompt_submit [hooks: 1]")
	frontend.updateHover(4, 3, 80, 16)
	if !strings.Contains(frontend.hoverText, "hook user_prompt_submit") {
		t.Fatalf("hover detail = %q", frontend.hoverText)
	}
}

func TestScreenRendersMarkdownAndMermaidPreview(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	frontend.screenAdd(screenAssistant, "# Ready\n\n```go\nfmt.Println(\"ok\")\n```\n\n```mermaid\nflowchart LR\nA[Start] --> B[Ship]\n```")
	plain := stripANSI(frontend.screenFrame(100, 24))
	for _, expected := range []string{"Ready", "fmt.Println(\"ok\")", "Start ──▶ Ship", "mermaid"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("rich text frame missing %q: %q", expected, plain)
		}
	}
}

func TestScreenSelectionCopiesRenderedContent(t *testing.T) {
	var copied string
	frontend := &ui{
		screenMode: true,
		cwd:        "/tmp/project",
		clipboard: func(value string) error {
			copied = value
			return nil
		},
	}
	frontend.screenAdd(screenAssistant, "```go\nfmt.Println(\"ok\")\n```\n\n```mermaid\nflowchart LR\nA[Start] --> B[Ship]\n```")
	rows := frontend.screenTranscriptRows(100)
	start, end := -1, -1
	for index, row := range rows {
		if strings.Contains(row.copyText, "fmt.Println") {
			start = index
		}
		if strings.Contains(row.copyText, "Start ──▶ Ship") {
			end = index
		}
	}
	if start < 0 || end < start {
		t.Fatalf("selection rows missing: start=%d end=%d rows=%#v", start, end, rows)
	}
	if !frontend.beginScreenSelection(rows[start].copyStart+1, start+3, 100, 24) {
		t.Fatal("selection did not start on transcript")
	}
	if !frontend.updateScreenSelection(rows[end].copyStart+displayWidth(rows[end].copyText), end+3, 100, 24) {
		t.Fatal("selection did not update on transcript")
	}
	if !strings.Contains(frontend.screenFrame(100, 24), "\x1b[7m") {
		t.Fatal("selected transcript row has no inverse highlight")
	}
	if err := frontend.finishScreenSelection(rows[end].copyStart+displayWidth(rows[end].copyText), end+3, 100, 24); err != nil {
		t.Fatalf("finish selection error = %v", err)
	}
	if !strings.Contains(copied, "fmt.Println(\"ok\")") || !strings.Contains(copied, "Start ──▶ Ship") {
		t.Fatalf("copied text = %q", copied)
	}
	if strings.Contains(copied, "┌") || strings.Contains(copied, "\x1b[") {
		t.Fatalf("copied text contains screen decoration: %q", copied)
	}
}

func TestReasoningIsOneTransientAnimatedStatus(t *testing.T) {
	frontend := &ui{screenMode: true, thread: threadSummary{ID: "thread-1"}}
	reasoning := json.RawMessage(`{"threadId":"thread-1","item":{"type":"reasoning"}}`)
	frontend.handleItemStarted(reasoning)
	if !frontend.reasoningActive || !frontend.reasoningShown {
		t.Fatalf("reasoning state = active:%t shown:%t", frontend.reasoningActive, frontend.reasoningShown)
	}
	if plain := stripANSI(frontend.screenFrame(80, 16)); !strings.Contains(plain, "thinking") {
		t.Fatalf("thinking status missing: %q", plain)
	}
	frontend.reasoningFrame = 6
	if plain := stripANSI(frontend.screenFrame(80, 16)); !strings.Contains(plain, "thinking.") {
		t.Fatalf("thinking animation did not advance: %q", plain)
	}
	frontend.handleNotification(rpcMessage{
		Method: "item/agentMessage/delta",
		Params: json.RawMessage(`{"threadId":"thread-1","delta":"answer"}`),
	})
	if frontend.reasoningActive {
		t.Fatal("reasoning status remained active after answer started")
	}
	frontend.handleItemStarted(reasoning)
	if frontend.reasoningActive {
		t.Fatal("reasoning status was shown again in the same turn")
	}
}

func TestSuccessfulToolCompletionIsHidden(t *testing.T) {
	if got := toolCompletionStatus("completed", float64(0)); got != "" {
		t.Fatalf("successful completion status = %q, want empty", got)
	}
	if got := toolCompletionStatus("failed", float64(1)); got != "failed exit 1" {
		t.Fatalf("failed completion status = %q", got)
	}
}

func TestCompletedToolActivityCollapsesIntoOneHiddenRow(t *testing.T) {
	frontend := &ui{screenMode: true, thread: threadSummary{ID: "thread-1"}}
	frontend.handleItemStarted(json.RawMessage(`{"threadId":"thread-1","item":{"type":"commandExecution","command":"first"}}`))
	frontend.handleItemCompleted(json.RawMessage(`{"threadId":"thread-1","item":{"type":"commandExecution","status":"failed","exitCode":1}}`))
	frontend.handleItemStarted(json.RawMessage(`{"threadId":"thread-1","item":{"type":"commandExecution","command":"second"}}`))
	frontend.handleItemCompleted(json.RawMessage(`{"threadId":"thread-1","item":{"type":"commandExecution","status":"failed","exitCode":2}}`))

	collapsed := stripANSI(frontend.screenFrame(100, 20))
	if strings.Count(collapsed, "Ctrl+O to show") != 1 {
		t.Fatalf("completed tool activity rendered %d collapsed rows: %q", strings.Count(collapsed, "Ctrl+O to show"), collapsed)
	}
	for _, hidden := range []string{"$ first", "$ second", "failed exit 1", "failed exit 2"} {
		if strings.Contains(collapsed, hidden) {
			t.Fatalf("collapsed tool activity exposed %q: %q", hidden, collapsed)
		}
	}

	frontend.showToolOutput = true
	expanded := stripANSI(frontend.screenFrame(100, 20))
	for _, visible := range []string{"$ first", "$ second", "failed exit 1", "failed exit 2"} {
		if !strings.Contains(expanded, visible) {
			t.Fatalf("expanded tool activity is missing %q: %q", visible, expanded)
		}
	}
}

func TestToolGroupClickExpandsOnlySelectedGroup(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	frontend.screenAddForTurn(screenTool, "$ first", "turn-1", "turn-1")
	frontend.screenAddForTurn(screenTool, "output\nfirst output", "turn-1", "turn-1")
	frontend.screenAddToolActivityStatus("failed exit 1", "turn-1", "turn-1")
	frontend.screenAddForTurn(screenAssistant, "answer", "turn-1", "turn-1")
	frontend.screenAddForTurn(screenTool, "$ second", "turn-2", "turn-2")
	frontend.screenAddForTurn(screenTool, "output\nsecond output", "turn-2", "turn-2")

	rows := frontend.screenTranscriptRows(100)
	firstGroupRow, secondGroupRow := -1, -1
	var firstGroup, secondGroup string
	for index, row := range rows {
		if row.toolGroup == "" {
			continue
		}
		if firstGroup == "" {
			firstGroupRow, firstGroup = index, row.toolGroup
			continue
		}
		if row.toolGroup != firstGroup {
			secondGroupRow, secondGroup = index, row.toolGroup
			break
		}
	}
	if firstGroupRow < 0 || secondGroupRow < 0 || firstGroup == secondGroup {
		t.Fatalf("tool group rows = %#v, groups = (%q, %q)", rows, firstGroup, secondGroup)
	}
	collapsed := stripANSI(frontend.screenFrame(100, 40))
	if strings.Contains(collapsed, "$ first") || strings.Contains(collapsed, "$ second") {
		t.Fatalf("collapsed tool details leaked: %q", collapsed)
	}

	clickX, clickY := 5, firstGroupRow+3
	if !frontend.beginScreenSelection(clickX, clickY, 100, 40) {
		t.Fatalf("could not start tool-group click at (%d, %d)", clickX, clickY)
	}
	if err := frontend.finishScreenSelection(clickX, clickY, 100, 40); err != nil {
		t.Fatalf("tool-group click error = %v", err)
	}
	if !frontend.expandedToolGroups[firstGroup] || frontend.expandedToolGroups[secondGroup] {
		t.Fatalf("expanded tool groups = %#v, want only %q", frontend.expandedToolGroups, firstGroup)
	}
	expanded := stripANSI(frontend.screenFrame(100, 40))
	if !strings.Contains(expanded, "$ first") || strings.Contains(expanded, "$ second") {
		t.Fatalf("per-group expansion rendered wrong detail: %q", expanded)
	}
}

func TestCanonicalTranscriptOutlivesBoundedScreenProjection(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	entryCount := maxScreenBlocks + 4
	for index := 0; index < entryCount; index++ {
		frontend.screenAdd(screenAssistant, fmt.Sprintf("answer-%03d", index))
	}

	if len(frontend.transcript.entries) != entryCount {
		t.Fatalf("canonical entry count = %d, want %d", len(frontend.transcript.entries), entryCount)
	}
	if len(frontend.screenBlocks) != maxScreenBlocks {
		t.Fatalf("screen projection count = %d, want %d", len(frontend.screenBlocks), maxScreenBlocks)
	}
	if frontend.transcript.entries[0].order != 1 || frontend.transcript.entries[entryCount-1].order != uint64(entryCount) {
		t.Fatalf("canonical order = (%d, %d), want (1, %d)", frontend.transcript.entries[0].order, frontend.transcript.entries[entryCount-1].order, entryCount)
	}
	if frontend.transcript.entries[0].id == frontend.transcript.entries[1].id {
		t.Fatal("canonical entry ids are not unique")
	}
	if !strings.Contains(frontend.screenBlocks[0].text, "answer-004") {
		t.Fatalf("bounded projection started with %q, want answer-004", frontend.screenBlocks[0].text)
	}

	frontend.screenAppend(screenAssistant, " tail")
	last := &frontend.transcript.entries[len(frontend.transcript.entries)-1]
	if last.text != "answer-259 tail" {
		t.Fatalf("canonical append text = %q, want answer-259 tail", last.text)
	}
}

func TestConnectionStatusUpdatesCanonicalEntry(t *testing.T) {
	frontend := &ui{screenMode: true}
	frontend.screenSetConnectionStatus(screenError, "connection lost")
	frontend.screenSetConnectionStatus(screenWarning, "reconnecting")

	if len(frontend.transcript.entries) != 1 {
		t.Fatalf("connection status entries = %d, want 1", len(frontend.transcript.entries))
	}
	entry := frontend.transcript.entries[0]
	if entry.kind != screenWarning || entry.role != screenWarning || entry.text != "reconnecting" {
		t.Fatalf("connection status entry = %#v", entry)
	}
	if len(frontend.screenBlocks) != 1 || frontend.screenBlocks[0].text != "reconnecting" {
		t.Fatalf("connection status projection = %#v", frontend.screenBlocks)
	}
}

func TestInlineEventsUseCanonicalTranscriptOrdering(t *testing.T) {
	var output bytes.Buffer
	frontend := &ui{
		thread: threadSummary{ID: "thread-1"},
		out:    &output,
	}
	frontend.renderHistoryItem(historyItem{
		Type:    "userMessage",
		Content: []historyInput{{Type: "text", Text: "previous question"}},
	})
	frontend.renderHistoryItem(historyItem{Type: "agentMessage", Text: "previous answer"})
	frontend.beginScreenTurn("turn-1")
	frontend.handleNotification(rpcMessage{
		Method: "item/agentMessage/delta",
		Params: json.RawMessage(`{"threadId":"thread-1","delta":"streamed answer"}`),
	})

	if len(frontend.transcript.entries) != 3 {
		t.Fatalf("canonical inline entries = %#v", frontend.transcript.entries)
	}
	if frontend.transcript.entries[0].kind != screenUser || frontend.transcript.entries[1].kind != screenAssistant || frontend.transcript.entries[2].kind != screenAssistant {
		t.Fatalf("canonical inline roles = %#v", frontend.transcript.entries)
	}
	if frontend.transcript.entries[2].text != "streamed answer" {
		t.Fatalf("canonical streamed text = %q", frontend.transcript.entries[2].text)
	}
}

func TestScreenReaderProjectionUsesCanonicalRoleLabels(t *testing.T) {
	frontend := &ui{screenMode: true}
	frontend.screenAdd(screenUser, "hello")
	frontend.screenAdd(screenAssistant, "world")
	frontend.screenAdd(screenTool, "$ pwd")
	frontend.screenAdd(screenPlan, "[done] inspect")

	lines := frontend.screenReaderProjection()
	plain := strings.Join(lines, "\n")
	for _, expected := range []string{"you: hello", "assistant: world", "tool: $ pwd", "plan: [done] inspect"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("screen-reader projection missing %q: %q", expected, plain)
		}
	}
	if strings.Contains(plain, "\x1b[") || strings.Contains(plain, "┌") {
		t.Fatalf("screen-reader projection contains visual decoration: %q", plain)
	}
}

func TestToolGroupStopsAtApprovalBoundary(t *testing.T) {
	frontend := &ui{screenMode: true}
	frontend.screenAddForTurn(screenTool, "$ before", "turn-1", "turn-1")
	firstGroup := frontend.transcript.entries[0].toolGroup
	frontend.showApproval(serverRequest{}, "touch x", "/tmp/project", "needs approval", []string{"accept"})
	frontend.approval = nil
	frontend.screenAddForTurn(screenTool, "$ after", "turn-1", "turn-1")

	if firstGroup == "" || frontend.transcript.entries[len(frontend.transcript.entries)-1].toolGroup == firstGroup {
		t.Fatalf("tool group crossed approval boundary: entries = %#v", frontend.transcript.entries)
	}
}

func TestToolGroupStopsAtErrorBoundary(t *testing.T) {
	frontend := &ui{screenMode: true}
	frontend.screenAddForTurn(screenTool, "$ before", "turn-1", "turn-1")
	firstGroup := frontend.transcript.entries[0].toolGroup
	frontend.screenAddForTurn(screenError, "transport error", "turn-1", "turn-1")
	frontend.screenAddForTurn(screenTool, "$ after", "turn-1", "turn-1")

	if firstGroup == "" || frontend.transcript.entries[len(frontend.transcript.entries)-1].toolGroup == firstGroup {
		t.Fatalf("tool group crossed error boundary: entries = %#v", frontend.transcript.entries)
	}
}

func TestToolGroupStopsAtUserInputBoundary(t *testing.T) {
	frontend := &ui{
		screenMode: true,
		thread:     threadSummary{ID: "thread-1"},
		turnID:     "turn-1",
		client:     &appServer{},
	}
	frontend.beginScreenTurn("turn-1")
	frontend.screenAdd(screenTool, "$ before")
	firstGroup := frontend.transcript.entries[0].toolGroup
	frontend.handleServerRequest(serverRequest{
		method: "item/tool/requestUserInput",
		params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","questions":[{"id":"mode","question":"Choose"}]}`),
	})
	frontend.inputRequest = nil
	frontend.screenAdd(screenTool, "$ after")

	if firstGroup == "" || frontend.transcript.entries[len(frontend.transcript.entries)-1].toolGroup == firstGroup {
		t.Fatalf("tool group crossed user-input boundary: entries = %#v", frontend.transcript.entries)
	}
}

func TestReconnectReplayDeduplicatesPartialTurn(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	frontend.beginScreenTurn("turn-1")
	frontend.screenAdd(screenUser, "question")
	frontend.bindActiveScreenTurn("turn-1")
	frontend.screenAppend(screenAssistant, "partial")
	frontend.screenSetConnectionStatus(screenError, "connection lost")

	client := newAppServer(&bufferWriteCloser{Buffer: &bytes.Buffer{}}, io.NopCloser(strings.NewReader("")), &exec.Cmd{})
	t.Cleanup(client.Close)
	thread := threadSummary{
		ID:  "thread-1",
		CWD: "/tmp/project",
		Turns: []historyTurn{{
			ID: "turn-1",
			Items: []historyItem{
				{Type: "userMessage", Content: []historyInput{{Type: "text", Text: "question"}}},
				{Type: "agentMessage", Text: "partial answer"},
				{Type: "commandExecution", Command: "pwd", AggregatedOutput: "/tmp/project", Status: "completed", ExitCode: float64(0)},
			},
		}},
	}
	frontend.adoptReconnected(client, thread)

	userCount, assistantCount, toolCount := 0, 0, 0
	firstErrorIndex := len(frontend.transcript.entries)
	for index, entry := range frontend.transcript.entries {
		if entry.kind == screenError && firstErrorIndex == len(frontend.transcript.entries) {
			firstErrorIndex = index
		}
		switch entry.kind {
		case screenUser:
			userCount++
		case screenAssistant:
			assistantCount++
			if entry.text != "partial answer" {
				t.Fatalf("replayed assistant text = %q", entry.text)
			}
		case screenTool:
			toolCount++
			if index > firstErrorIndex {
				t.Fatalf("replayed tool entry remained after connection status: %#v", frontend.transcript.entries)
			}
		}
	}
	if userCount != 1 || assistantCount != 1 || toolCount != 2 {
		t.Fatalf("replay counts = user:%d assistant:%d tool:%d, want 1/1/2", userCount, assistantCount, toolCount)
	}
}

func TestScreenViewportCanPageBack(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	for index := 0; index < 12; index++ {
		frontend.screenAdd(screenAssistant, fmt.Sprintf("line-%02d", index))
	}
	latest := stripANSI(frontend.screenFrame(80, 12))
	if !strings.Contains(latest, "line-11") || strings.Contains(latest, "line-00") {
		t.Fatalf("default viewport is not at latest output: %q", latest)
	}
	_, bodyRows := frontend.screenComposerAndBodyRows(80, 12)
	maxOffset := len(frontend.screenTranscriptRows(80)) - bodyRows
	frontend.scrollOffset = maxOffset
	earlier := stripANSI(frontend.screenFrame(80, 12))
	if !strings.Contains(earlier, "line-00") || strings.Contains(earlier, "line-11") {
		t.Fatalf("paged viewport did not move back: %q", earlier)
	}
}

func TestScreenViewportStopsAtTranscriptBounds(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	for index := 0; index < 12; index++ {
		frontend.screenAdd(screenAssistant, fmt.Sprintf("line-%02d", index))
	}

	frontend.scrollOffset = 999
	frontend.screenFrame(80, 12)
	_, bodyRows := frontend.screenComposerAndBodyRows(80, 12)
	maxOffset := len(frontend.screenTranscriptRows(80)) - bodyRows
	if frontend.scrollOffset != maxOffset {
		t.Fatalf("top scroll offset = %d, want %d visible rows from the bottom", frontend.scrollOffset, maxOffset)
	}
	frontend.scrollOffset = -1
	frontend.screenFrame(80, 12)
	if frontend.scrollOffset != 0 {
		t.Fatalf("bottom scroll offset = %d, want 0", frontend.scrollOffset)
	}

	frontend.scrollOffset = maxOffset
	frontend.scrollScreenBy(3, 80, 12)
	if frontend.scrollOffset != maxOffset {
		t.Fatalf("wheel at top changed scroll offset to %d, want %d", frontend.scrollOffset, maxOffset)
	}
	frontend.scrollScreenBy(-99, 80, 12)
	if frontend.scrollOffset != 0 {
		t.Fatalf("wheel past bottom changed scroll offset to %d, want 0", frontend.scrollOffset)
	}

	frontend.scrollOffset = 3
	frontend.screenAdd(screenAssistant, "new event")
	if frontend.scrollOffset == 0 {
		t.Fatal("new transcript event forced a scrolled-away viewport back to bottom")
	}
}

func TestScreenFrameClampsFailedResizeDimensions(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project", input: []rune("draft"), cursor: 5}
	frontend.screenAdd(screenAssistant, "answer")
	frame := stripANSI(frontend.screenFrame(1, 1))
	if lines := strings.Split(frame, "\r\n"); len(lines) != 8 {
		t.Fatalf("clamped screen frame lines = %d, want 8", len(lines))
	}
	if !strings.Contains(frame, "draft") || !strings.Contains(frame, "carolline") {
		t.Fatalf("clamped screen frame lost mounted state: %q", frame)
	}
}

func TestScreenCollapsesToolOutputByDefault(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	frontend.screenAdd(screenTool, "$ rg -n TODO .")
	frontend.screenAdd(screenTool, "output\nline-one\nline-two")

	collapsed := stripANSI(frontend.screenFrame(80, 16))
	if strings.Contains(collapsed, "line-one") || strings.Contains(collapsed, "$ rg -n TODO .") || strings.Count(collapsed, "tools hidden") != 1 {
		t.Fatalf("tool use was not collapsed: %q", collapsed)
	}
	frontend.showToolOutput = true
	expanded := stripANSI(frontend.screenFrame(80, 16))
	if !strings.Contains(expanded, "line-one") {
		t.Fatalf("expanded tool output is missing: %q", expanded)
	}
}

func TestScreenHistoryToolOutputUsesCollapsedBlock(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	frontend.renderHistoryItem(historyItem{
		Type:             "commandExecution",
		Command:          "rg TODO",
		AggregatedOutput: "history-output-line",
		Status:           "completed",
	})
	collapsed := stripANSI(frontend.screenFrame(80, 16))
	if strings.Contains(collapsed, "history-output-line") || strings.Contains(collapsed, "$ rg TODO") || strings.Count(collapsed, "tools hidden") != 1 {
		t.Fatalf("history tool use was not collapsed: %q", collapsed)
	}
}

func TestTurnFinishAddsRecap(t *testing.T) {
	frontend := &ui{
		screenMode: true,
		busy:       true,
		recap: turnRecap{
			commands:  []string{"pwd"},
			toolCount: 1,
			outputs:   1,
		},
	}
	frontend.finishTurn("completed")
	plain := stripANSI(frontend.screenFrame(80, 16))
	for _, expected := range []string{"recap", "1 tool", "turn completed"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("recap missing %q: %q", expected, plain)
		}
	}
	if strings.Contains(plain, "$ pwd") {
		t.Fatalf("collapsed recap exposed command detail: %q", plain)
	}
}

func TestScreenApprovalComposer(t *testing.T) {
	frontend := &ui{
		screenMode: true,
		cwd:        "/tmp/project",
		thread:     threadSummary{Model: "gpt-test", ReasoningEffort: "high", ApprovalPolicy: "on-request"},
		approval: &approvalRequest{
			action:    "touch x",
			cwd:       "/tmp/project",
			reason:    "requested by the turn",
			available: []string{"accept", "decline"},
		},
	}
	plain := stripANSI(frontend.screenFrame(80, 16))
	for _, expected := range []string{"approval required", "touch x", "requested by the turn", "[a]ccept  [d]ecline", "Esc cancel"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("approval frame missing %q: %q", expected, plain)
		}
	}
}

func TestScreenShortcutOverlay(t *testing.T) {
	frontend := &ui{screenMode: true, shortcuts: true, cwd: "/tmp/project"}
	plain := stripANSI(frontend.screenFrame(80, 16))
	for _, expected := range []string{"shortcuts", "Enter send", "Alt+Enter newline", "Ctrl-C twice quit", "/help"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("shortcut frame missing %q: %q", expected, plain)
		}
	}
}

func TestHeaderOmitsUnnamedLabel(t *testing.T) {
	var output bytes.Buffer
	frontend := &ui{
		thread: threadSummary{
			ID:              "thread-1",
			Model:           "gpt-test",
			ReasoningEffort: "medium",
			ApprovalPolicy:  "on-request",
			Sandbox:         json.RawMessage("{\"type\":\"readOnly\"}"),
		},
		cwd: "/tmp/project",
		out: &output,
	}
	frontend.renderHeader()
	if strings.Contains(output.String(), "name unnamed") {
		t.Fatalf("header should omit unnamed thread: %q", output.String())
	}
	if !strings.Contains(output.String(), "sandbox") || !strings.Contains(output.String(), "Alt+Enter") || !strings.Contains(output.String(), "inline scrollback fallback") {
		t.Fatalf("header lost metadata or controls: %q", output.String())
	}
}

func TestReadKeysParsesEditingSequences(t *testing.T) {
	input := strings.NewReader("a\x1b[A\x1b\r\x7f\x03\x18")
	keys := make(chan keyEvent, 8)
	go readKeys(input, keys)

	var got []keyType
	for key := range keys {
		got = append(got, key.typ)
	}
	want := []keyType{keyRune, keyUp, keyAltEnter, keyBackspace, keyCtrlC, keyCtrlX}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("readKeys() = %v, want %v", got, want)
	}
}

func TestReadKeysParsesPagingSequences(t *testing.T) {
	input := strings.NewReader("\x1b[5~\x1b[6~")
	keys := make(chan keyEvent, 2)
	go readKeys(input, keys)

	var got []keyType
	for key := range keys {
		got = append(got, key.typ)
	}
	want := []keyType{keyPageUp, keyPageDown}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("readKeys() = %v, want %v", got, want)
	}
}

func TestReadKeysParsesMouseWheelSequences(t *testing.T) {
	input := strings.NewReader("\x1b[<64;40;12M\x1b[<65;40;12M")
	keys := make(chan keyEvent, 2)
	go readKeys(input, keys)

	var got []keyType
	for key := range keys {
		got = append(got, key.typ)
	}
	want := []keyType{keyScrollUp, keyScrollDown}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("readKeys() = %v, want %v", got, want)
	}
}

func TestScreenToolOutputShortcutTogglesExpansion(t *testing.T) {
	frontend := &ui{screenMode: true}
	if err := frontend.handleKey(keyEvent{typ: keyCtrlO}); err != nil {
		t.Fatalf("handleKey(Ctrl+O) error = %v", err)
	}
	if !frontend.showToolOutput {
		t.Fatal("Ctrl+O did not expand tool output")
	}
}

func TestApprovalDecisionsRespectServerOptions(t *testing.T) {
	available := []string{"accept", "decline"}
	if !decisionAllowed(available, "accept") {
		t.Fatal("accept should be allowed")
	}
	if decisionAllowed(available, "cancel") {
		t.Fatal("cancel should not be allowed")
	}
	if got := approvalHint(available); got != "[a]ccept  [d]ecline" {
		t.Fatalf("approvalHint() = %q", got)
	}
}

func TestApprovalRequestRoundTrip(t *testing.T) {
	var rpcOutput bytes.Buffer
	var uiOutput bytes.Buffer
	client := newAppServer(&bufferWriteCloser{Buffer: &rpcOutput}, io.NopCloser(strings.NewReader("")), &exec.Cmd{})
	frontend := &ui{
		client: client,
		thread: threadSummary{ID: "thread-1"},
		cwd:    "/tmp/project",
		busy:   true,
		out:    &uiOutput,
	}
	frontend.handleServerRequest(serverRequest{
		id:     json.RawMessage("3"),
		method: "item/commandExecution/requestApproval",
		params: json.RawMessage("{\"threadId\":\"thread-1\",\"command\":\"touch x\",\"cwd\":\"/tmp/project\",\"availableDecisions\":[\"accept\",\"decline\"]}"),
	})
	if frontend.approval == nil || !strings.Contains(uiOutput.String(), "touch x") {
		t.Fatalf("approval was not rendered: approval=%#v output=%q", frontend.approval, uiOutput.String())
	}
	if err := frontend.handleApprovalKey(keyEvent{typ: keyRune, rune: 'a'}); err != nil {
		t.Fatalf("handleApprovalKey() error = %v", err)
	}

	var response struct {
		ID     int
		Result map[string]string
	}
	if err := json.Unmarshal(bytes.TrimSpace(rpcOutput.Bytes()), &response); err != nil {
		t.Fatalf("decode approval response: %v", err)
	}
	if response.ID != 3 || response.Result["decision"] != "accept" {
		t.Fatalf("approval response = %#v", response)
	}
}

func TestPermissionRequestFailsClosed(t *testing.T) {
	var rpcOutput bytes.Buffer
	frontend := &ui{
		client:     newAppServer(&bufferWriteCloser{Buffer: &rpcOutput}, io.NopCloser(strings.NewReader("")), &exec.Cmd{}),
		thread:     threadSummary{ID: "thread-1"},
		screenMode: true,
		cwd:        "/tmp/project",
	}
	frontend.handleServerRequest(serverRequest{
		id:     json.RawMessage("4"),
		method: "item/permissions/requestApproval",
		params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1","cwd":"/tmp/project","permissions":{"fileSystem":{"write":["/tmp/project"]}},"startedAtMs":1}`),
	})

	var response struct {
		ID     int `json:"id"`
		Result struct {
			Permissions map[string]any `json:"permissions"`
			Scope       string         `json:"scope"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(rpcOutput.Bytes()), &response); err != nil {
		t.Fatalf("decode permission response: %v", err)
	}
	if response.ID != 4 || response.Result.Scope != "turn" || len(response.Result.Permissions) != 0 {
		t.Fatalf("permission response = %#v", response)
	}
	plain := stripANSI(frontend.screenFrame(80, 16))
	if !strings.Contains(plain, "permission request denied") {
		t.Fatalf("permission denial was not visible: %q", plain)
	}
}

func TestToolRequestUserInputRoundTrip(t *testing.T) {
	var rpcOutput bytes.Buffer
	frontend := &ui{
		client:     newAppServer(&bufferWriteCloser{Buffer: &rpcOutput}, io.NopCloser(strings.NewReader("")), &exec.Cmd{}),
		thread:     threadSummary{ID: "thread-1"},
		turnID:     "turn-1",
		busy:       true,
		screenMode: true,
		cwd:        "/tmp/project",
	}
	frontend.handleServerRequest(serverRequest{
		id:     json.RawMessage("5"),
		method: "item/tool/requestUserInput",
		params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1","isBlocking":true,"questions":[{"header":"Name","id":"name","question":"What should I call you?","options":[{"label":"Miyago","description":"use the current name"}]},{"header":"Ready","id":"ready","question":"Ready to continue?"}]}`),
	})
	if frontend.inputRequest == nil {
		t.Fatal("user input request was not installed")
	}
	if plain := stripANSI(frontend.screenFrame(80, 16)); !strings.Contains(plain, "What should I call you?") {
		t.Fatalf("user input question was not rendered: %q", plain)
	}

	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatalf("submit selected user input error = %v", err)
	}
	if frontend.inputRequest == nil || frontend.inputRequest.index != 1 {
		t.Fatalf("user input did not advance to the next question: %#v", frontend.inputRequest)
	}
	for _, character := range "yes" {
		if err := frontend.handleKey(keyEvent{typ: keyRune, rune: character}); err != nil {
			t.Fatalf("handleKey(%q) error = %v", character, err)
		}
	}
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatalf("submit second user input error = %v", err)
	}

	var response struct {
		ID     int `json:"id"`
		Result struct {
			Answers map[string]struct {
				Answers []string `json:"answers"`
			} `json:"answers"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(rpcOutput.Bytes()), &response); err != nil {
		t.Fatalf("decode user input response: %v", err)
	}
	if response.ID != 5 || strings.Join(response.Result.Answers["name"].Answers, ",") != "Miyago" || strings.Join(response.Result.Answers["ready"].Answers, ",") != "yes" {
		t.Fatalf("user input response = %#v", response)
	}
}

func TestToolRequestUserInputNavigatesOptionsAndOther(t *testing.T) {
	var rpcOutput bytes.Buffer
	frontend := &ui{
		client:     newAppServer(&bufferWriteCloser{Buffer: &rpcOutput}, io.NopCloser(strings.NewReader("")), &exec.Cmd{}),
		thread:     threadSummary{ID: "thread-1"},
		turnID:     "turn-1",
		busy:       true,
		screenMode: true,
		cwd:        "/tmp/project",
	}
	frontend.handleServerRequest(serverRequest{
		id:     json.RawMessage("7"),
		method: "item/tool/requestUserInput",
		params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","questions":[{"header":"Mode","id":"mode","question":"Choose a mode","options":[{"label":"Alpha","description":"first"},{"label":"Beta","description":"second"}],"isOther":true}]}`),
	})
	plain := stripANSI(frontend.screenFrame(100, 24))
	if !strings.Contains(plain, "1) Alpha") || !strings.Contains(plain, "2) Beta") || !strings.Contains(plain, "o) Other") {
		t.Fatalf("input options were not rendered separately: %q", plain)
	}
	if err := frontend.handleKey(keyEvent{typ: keyDown}); err != nil {
		t.Fatalf("select second option error = %v", err)
	}
	if plain := stripANSI(frontend.screenFrame(100, 24)); !strings.Contains(plain, "› 2) Beta") {
		t.Fatalf("second option was not selected: %q", plain)
	}
	if err := frontend.handleKey(keyEvent{typ: keyDown}); err != nil {
		t.Fatalf("select other option error = %v", err)
	}
	for _, character := range "custom answer" {
		if err := frontend.handleKey(keyEvent{typ: keyRune, rune: character}); err != nil {
			t.Fatalf("type other answer error = %v", err)
		}
	}
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatalf("submit other answer error = %v", err)
	}
	var response struct {
		ID     int `json:"id"`
		Result struct {
			Answers map[string]struct {
				Answers []string `json:"answers"`
			} `json:"answers"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(rpcOutput.Bytes()), &response); err != nil {
		t.Fatalf("decode other answer response: %v", err)
	}
	if response.ID != 7 || strings.Join(response.Result.Answers["mode"].Answers, ",") != "custom answer" {
		t.Fatalf("other answer response = %#v", response)
	}
}

func TestToolRequestUserInputSubmitsSelectedOption(t *testing.T) {
	var rpcOutput bytes.Buffer
	frontend := &ui{
		client:     newAppServer(&bufferWriteCloser{Buffer: &rpcOutput}, io.NopCloser(strings.NewReader("")), &exec.Cmd{}),
		thread:     threadSummary{ID: "thread-1"},
		turnID:     "turn-1",
		busy:       true,
		screenMode: true,
	}
	frontend.handleServerRequest(serverRequest{
		id:     json.RawMessage("8"),
		method: "item/tool/requestUserInput",
		params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","questions":[{"id":"mode","question":"Choose a mode","options":[{"label":"Alpha"},{"label":"Beta"}]}]}`),
	})
	if err := frontend.handleKey(keyEvent{typ: keyDown}); err != nil {
		t.Fatalf("select option error = %v", err)
	}
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatalf("submit option error = %v", err)
	}
	var response struct {
		Result struct {
			Answers map[string]struct {
				Answers []string `json:"answers"`
			} `json:"answers"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(rpcOutput.Bytes()), &response); err != nil {
		t.Fatalf("decode option response: %v", err)
	}
	if strings.Join(response.Result.Answers["mode"].Answers, ",") != "Beta" {
		t.Fatalf("selected option response = %#v", response)
	}
}

func TestInlineUserInputOptionsShowSelectionState(t *testing.T) {
	var output bytes.Buffer
	frontend := &ui{
		out: &output,
		inputRequest: &userInputRequest{
			questions: []userInputQuestion{{
				Header:   "Mode",
				Question: "Choose a mode",
				Options:  []userInputOption{{Label: "Alpha"}, {Label: "Beta"}},
			}},
		},
	}
	frontend.renderPrompt()
	if !strings.Contains(output.String(), "› 1) Alpha") || !strings.Contains(output.String(), "2) Beta") {
		t.Fatalf("inline options were not rendered: %q", output.String())
	}
	if err := frontend.handleInputRequestKey(keyEvent{typ: keyDown}); err != nil {
		t.Fatalf("inline option navigation error = %v", err)
	}
	if !strings.Contains(output.String(), "› 2) Beta") {
		t.Fatalf("inline selected option was not rendered: %q", output.String())
	}
}

func TestSecretUserInputIsMasked(t *testing.T) {
	frontend := &ui{
		screenMode: true,
		input:      []rune("super-secret"),
		cursor:     len([]rune("super-secret")),
		inputRequest: &userInputRequest{
			questions: []userInputQuestion{{ID: "token", Question: "Enter token", IsSecret: true}},
		},
	}
	plain := stripANSI(frontend.screenFrame(80, 16))
	if strings.Contains(plain, "super-secret") || !strings.Contains(plain, "••••••••••••") {
		t.Fatalf("secret answer was rendered unsafely: %q", plain)
	}
	if strings.Contains(plain, "█") {
		t.Fatalf("secret answer rendered a width-consuming cursor: %q", plain)
	}
	if _, _, ok := frontend.screenCursorPosition(80, 16); !ok {
		t.Fatal("secret answer lost its native cursor position")
	}
}

func TestMismatchedServerRequestIsVisibleAndRejected(t *testing.T) {
	var rpcOutput bytes.Buffer
	frontend := &ui{
		client:     newAppServer(&bufferWriteCloser{Buffer: &rpcOutput}, io.NopCloser(strings.NewReader("")), &exec.Cmd{}),
		thread:     threadSummary{ID: "thread-1"},
		screenMode: true,
	}
	frontend.handleServerRequest(serverRequest{
		id:     json.RawMessage("6"),
		method: "item/commandExecution/requestApproval",
		params: json.RawMessage(`{"threadId":"other-thread","turnId":"turn-1","itemId":"item-1","command":"touch x"}`),
	})
	if frontend.approval != nil {
		t.Fatal("mismatched request should not open an approval card")
	}
	var response struct {
		ID    int `json:"id"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(rpcOutput.Bytes()), &response); err != nil {
		t.Fatalf("decode mismatch response: %v", err)
	}
	if response.ID != 6 || response.Error == nil || !strings.Contains(response.Error.Message, "thread") {
		t.Fatalf("mismatch response = %#v", response)
	}
	if plain := stripANSI(frontend.screenFrame(80, 16)); !strings.Contains(plain, "thread mismatch") {
		t.Fatalf("mismatch was not visible: %q", plain)
	}
}

func TestBuiltInCommands(t *testing.T) {
	var output bytes.Buffer
	frontend := &ui{
		thread: threadSummary{ID: "thread-1", Model: "gpt-test", ReasoningEffort: "medium", ApprovalPolicy: "on-request", Sandbox: json.RawMessage("{\"type\":\"read-only\"}")},
		cwd:    "/tmp/project",
		out:    &output,
	}
	if err := frontend.handleCommand("/help"); err != nil {
		t.Fatalf("/help error = %v", err)
	}
	if !strings.Contains(output.String(), "/status") || !strings.Contains(output.String(), "exit") {
		t.Fatalf("/help output = %q", output.String())
	}
	if err := frontend.handleCommand("/status"); err != nil {
		t.Fatalf("/status error = %v", err)
	}
	if !strings.Contains(output.String(), "gpt-test") {
		t.Fatalf("/status output = %q", output.String())
	}
	if err := frontend.handleCommand("/quit"); !errors.Is(err, errQuit) {
		t.Fatalf("/quit error = %v", err)
	}
}

func TestCtrlCRequiresConfirmationBeforeQuit(t *testing.T) {
	frontend := &ui{screenMode: true}
	if err := frontend.handleKey(keyEvent{typ: keyCtrlC}); err != nil {
		t.Fatalf("first Ctrl-C error = %v", err)
	}
	if frontend.lastCtrlC.IsZero() {
		t.Fatal("first Ctrl-C did not arm quit confirmation")
	}
	if plain := stripANSI(frontend.screenFrame(80, 16)); !strings.Contains(plain, "press Ctrl-C again to exit") {
		t.Fatalf("quit confirmation status missing: %q", plain)
	}
	if err := frontend.handleKey(keyEvent{typ: keyCtrlC}); !errors.Is(err, errQuit) {
		t.Fatalf("second Ctrl-C error = %v, want quit", err)
	}
}

func TestCtrlCConfirmationResetsAfterEditing(t *testing.T) {
	frontend := &ui{screenMode: true}
	if err := frontend.handleKey(keyEvent{typ: keyCtrlC}); err != nil {
		t.Fatalf("first Ctrl-C error = %v", err)
	}
	if err := frontend.handleKey(keyEvent{typ: keyRune, rune: 'x'}); err != nil {
		t.Fatalf("editing after Ctrl-C error = %v", err)
	}
	if err := frontend.handleKey(keyEvent{typ: keyCtrlC}); err != nil {
		t.Fatalf("Ctrl-C after editing error = %v, want confirmation restart", err)
	}
}

func TestExitInputWithoutSlashQuits(t *testing.T) {
	frontend := &ui{input: []rune("exit"), cursor: len([]rune("exit"))}
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); !errors.Is(err, errQuit) {
		t.Fatalf("plain exit error = %v, want quit", err)
	}
}

func BenchmarkScreenFrameLongTranscript(b *testing.B) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	for index := 0; index < maxScreenBlocks*4; index++ {
		frontend.screenAdd(screenAssistant, fmt.Sprintf("long-answer-%04d: inspect current state and preserve the useful context", index))
	}
	frontend.input = []rune("draft")
	frontend.cursor = len(frontend.input)
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		_ = frontend.screenFrame(120, 32)
	}
}

func stripANSI(value string) string {
	var builder strings.Builder
	inside := false
	csi := false
	for _, character := range value {
		if character == '\x1b' {
			inside = true
			csi = false
			continue
		}
		if inside {
			if character == '[' {
				csi = true
				continue
			}
			if csi {
				if character >= '@' && character <= '~' {
					inside = false
					csi = false
				}
				continue
			}
			if character >= '@' && character <= '~' {
				inside = false
			}
			continue
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

type respondingWriter struct {
	output *io.PipeWriter
}

func (w *respondingWriter) Write(value []byte) (int, error) {
	var request struct {
		ID int64
	}
	if err := json.Unmarshal(bytes.TrimSpace(value), &request); err != nil {
		return 0, fmt.Errorf("decode request: %w", err)
	}
	response := fmt.Sprintf("{\"id\":%d,\"result\":{\"value\":\"ok\"}}\n", request.ID)
	if _, err := io.WriteString(w.output, response); err != nil {
		return 0, err
	}
	return len(value), nil
}

func (w *respondingWriter) Close() error {
	return w.output.Close()
}

type bufferWriteCloser struct {
	*bytes.Buffer
	mu     sync.Mutex
	closed bool
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("fixture terminal write failure")
}

func (w *bufferWriteCloser) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	return w.Buffer.Write(value)
}

func (w *bufferWriteCloser) Close() error {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	return nil
}
