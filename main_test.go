package main

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
	if got := string(frontend.input); got != "second" {
		t.Fatalf("first history up = %q, want second", got)
	}
	if err := frontend.handleKey(keyEvent{typ: keyUp}); err != nil {
		t.Fatalf("second history up error = %v", err)
	}
	if got := string(frontend.input); got != "first" {
		t.Fatalf("second history up = %q, want first", got)
	}
	if err := frontend.handleKey(keyEvent{typ: keyDown}); err != nil {
		t.Fatalf("history down error = %v", err)
	}
	if got := string(frontend.input); got != "second" {
		t.Fatalf("history down = %q, want second", got)
	}
	if err := frontend.handleKey(keyEvent{typ: keyDown}); err != nil {
		t.Fatalf("history restore error = %v", err)
	}
	if got := string(frontend.input); got != "draft" {
		t.Fatalf("history restore = %q, want draft", got)
	}
}

func TestMouseEditingEvents(t *testing.T) {
	input := strings.NewReader("\x1b[<0;8;14M\x1b[<35;8;14M\x1b[<0;8;14m")
	keys := make(chan keyEvent, 2)
	go readKeys(input, keys)

	var got []keyEvent
	for key := range keys {
		got = append(got, key)
	}
	if len(got) != 2 || got[0].typ != keyMouseClick || got[0].mouseX != 8 || got[0].mouseY != 14 || got[1].typ != keyMouseMove {
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
	for _, expected := range []string{"recent history", "previous question", "previous answer", "$ pwd", "/tmp/project", "exit 0"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("history output missing %q: %q", expected, output.String())
		}
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
		"hi▌",
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

func TestScreenConversationUsesCard(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	frontend.screenAdd(screenUser, "hello")
	frontend.screenAdd(screenAssistant, "world")
	plain := stripANSI(frontend.screenFrame(80, 16))
	if !strings.Contains(plain, "┌─ conversation") || !strings.Contains(plain, "└") || !strings.Contains(plain, "│ › hello") {
		t.Fatalf("conversation card missing: %q", plain)
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
	for _, expected := range []string{"Ready", "fmt.Println(\"ok\")", "Start", "Ship", "mermaid"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("rich text frame missing %q: %q", expected, plain)
		}
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
	frontend.scrollOffset = 8
	earlier := stripANSI(frontend.screenFrame(80, 12))
	if !strings.Contains(earlier, "line-00") || strings.Contains(earlier, "line-11") {
		t.Fatalf("paged viewport did not move back: %q", earlier)
	}
}

func TestScreenCollapsesToolOutputByDefault(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project"}
	frontend.screenAdd(screenTool, "$ rg -n TODO .")
	frontend.screenAdd(screenTool, "output\nline-one\nline-two")

	collapsed := stripANSI(frontend.screenFrame(80, 16))
	if strings.Contains(collapsed, "line-one") || !strings.Contains(collapsed, "tool output hidden") {
		t.Fatalf("tool output was not collapsed: %q", collapsed)
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
	if strings.Contains(collapsed, "history-output-line") || !strings.Contains(collapsed, "tool output hidden") {
		t.Fatalf("history tool output was not collapsed: %q", collapsed)
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
	for _, expected := range []string{"recap", "1 tool", "$ pwd", "output collapsed", "turn completed"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("recap missing %q: %q", expected, plain)
		}
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
	for _, expected := range []string{"shortcuts", "Enter send", "Alt+Enter newline", "Ctrl-C interrupt/quit", "/help"} {
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
	if !strings.Contains(output.String(), "sandbox") || !strings.Contains(output.String(), "Alt+Enter") {
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

	for _, character := range "Miyago" {
		if err := frontend.handleKey(keyEvent{typ: keyRune, rune: character}); err != nil {
			t.Fatalf("handleKey(%q) error = %v", character, err)
		}
	}
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatalf("submit user input error = %v", err)
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
	if !strings.Contains(output.String(), "/status") {
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
