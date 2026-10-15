package lumen

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newFakeSession(t *testing.T, run *fakeACPRun) string {
	t.Helper()
	info, err := run.backend.NewSession(t.TempDir(), runtimeOverrides{})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	return info.ID
}

// T-P2-02：initialize 參數。
func TestACPInitializeParams(t *testing.T) {
	run := startFakeACP(t, nil, nil)
	params := fakeACPRequests(t, run.logPath, "initialize")
	if len(params) != 1 {
		t.Fatalf("initialize requests = %d", len(params))
	}
	var got struct {
		ProtocolVersion    int             `json:"protocolVersion"`
		ClientCapabilities json.RawMessage `json:"clientCapabilities"`
		ClientInfo         struct {
			Name string `json:"name"`
		} `json:"clientInfo"`
	}
	if err := json.Unmarshal(params[0], &got); err != nil {
		t.Fatal(err)
	}
	if got.ProtocolVersion != 1 || string(got.ClientCapabilities) != "{}" || got.ClientInfo.Name != "lumen" {
		t.Fatalf("initialize params = %s", params[0])
	}
}

// T-P2-03：protocolVersion 不是 1 -> 關閉 subprocess、顯示版本不符。
func TestACPProtocolVersionMismatch(t *testing.T) {
	cfg, _ := fakeACPConfig(t, map[string]string{"FAKEACP_PROTOCOL": "2"})
	backend := newACPBackend(cfg)
	t.Cleanup(backend.Close)
	err := backend.Open(t.Context())
	if err == nil || !strings.Contains(err.Error(), "protocol version 2") {
		t.Fatalf("Open() error = %v", err)
	}
	if disconnected, _ := backend.Disconnected(); !disconnected {
		t.Fatal("backend should be disconnected after version mismatch")
	}
	if _, err := backend.NewSession(t.TempDir(), runtimeOverrides{}); err == nil {
		t.Fatal("NewSession succeeded after version mismatch")
	}
	if strings.Contains(err.Error(), "SEKRIT") {
		t.Fatalf("error leaks args: %v", err)
	}
}

// T-P2-04：session/new 的 cwd 是絕對路徑、mcpServers 是空陣列。
func TestACPNewSessionParams(t *testing.T) {
	run := startFakeACP(t, nil, nil)
	workDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(workDir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info, err := run.backend.NewSession(relative, runtimeOverrides{})
	if err != nil || info.ID != "sess-new" {
		t.Fatalf("NewSession() = %#v, %v", info, err)
	}
	params := fakeACPRequests(t, run.logPath, "session/new")
	var got struct {
		CWD        string          `json:"cwd"`
		MCPServers json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(params[0], &got); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got.CWD) || string(got.MCPServers) != "[]" {
		t.Fatalf("session/new params = %s", params[0])
	}
	if info.CWD != got.CWD {
		t.Fatalf("SessionInfo.CWD = %q, want %q", info.CWD, got.CWD)
	}
}

// T-P2-05：load 重播成 history（不產生 live event）；resume 不重播；兩者皆無 -> 不支援。
func TestACPLoadReplaysHistoryWithoutEvents(t *testing.T) {
	run := startFakeACP(t, map[string]string{"FAKEACP_CAPS": "load,resume,list"}, nil)
	caps := run.backend.Caps()
	if !caps.SessionResume || !caps.ReplayOnResume || !caps.SessionList || caps.ForkAtTurn || caps.Goal || caps.Review || caps.Compact || caps.UserInput {
		t.Fatalf("caps = %#v", caps)
	}
	info, err := run.backend.ResumeSession("old-session", t.TempDir(), runtimeOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if len(fakeACPRequests(t, run.logPath, "session/load")) != 1 || len(fakeACPRequests(t, run.logPath, "session/resume")) != 0 {
		t.Fatal("expected session/load only")
	}
	if len(info.Turns) != 2 {
		t.Fatalf("turns = %#v", info.Turns)
	}
	first := info.Turns[0].Items
	if len(first) != 3 || first[0].Type != "userMessage" || historyText(first[0].Content) != "hello agent" ||
		first[1].Type != "agentMessage" || first[1].Text != "hi there" ||
		first[2].Type != "commandExecution" || first[2].Command != "ls" || first[2].AggregatedOutput != "file.txt\n" || first[2].Status != "completed" {
		t.Fatalf("first turn items = %#v", first)
	}
	if second := info.Turns[1].Items; len(second) != 2 || historyText(second[0].Content) != "second question" || second[1].Text != "second answer" {
		t.Fatalf("second turn items = %#v", second)
	}
	select {
	case event := <-run.events:
		t.Fatalf("replay produced a live event: %#v", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestACPResumeWithoutReplay(t *testing.T) {
	run := startFakeACP(t, map[string]string{"FAKEACP_CAPS": "resume"}, nil)
	caps := run.backend.Caps()
	if !caps.SessionResume || caps.ReplayOnResume || caps.SessionList {
		t.Fatalf("caps = %#v", caps)
	}
	info, err := run.backend.ResumeSession("old", t.TempDir(), runtimeOverrides{})
	if err != nil || info.ID != "old" || len(info.Turns) != 0 {
		t.Fatalf("ResumeSession() = %#v, %v", info, err)
	}
	if len(fakeACPRequests(t, run.logPath, "session/resume")) != 1 || len(fakeACPRequests(t, run.logPath, "session/load")) != 0 {
		t.Fatal("expected session/resume only")
	}
	if _, err := run.backend.ListSessions(t.TempDir(), 5); err == nil {
		t.Fatal("ListSessions succeeded without list capability")
	}
	if len(fakeACPRequests(t, run.logPath, "session/list")) != 0 {
		t.Fatal("session/list must not be called without the capability")
	}
}

func TestACPNoResumeCapability(t *testing.T) {
	run := startFakeACP(t, map[string]string{"FAKEACP_CAPS": "none"}, nil)
	if run.backend.Caps().SessionResume {
		t.Fatal("SessionResume should be false")
	}
	if _, err := run.backend.ResumeSession("old", t.TempDir(), runtimeOverrides{}); err == nil {
		t.Fatal("ResumeSession succeeded without load/resume capability")
	}
}

func TestACPLoadNotFoundMapsToSessionNotFound(t *testing.T) {
	run := startFakeACP(t, map[string]string{"FAKEACP_LOAD_ERROR": "1"}, nil)
	_, err := run.backend.ResumeSession("gone", t.TempDir(), runtimeOverrides{})
	if !errors.Is(err, errSessionNotFound) {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if !canStartFreshThreadAfterResume(err, false) || canStartFreshThreadAfterResume(err, true) {
		t.Fatal("fresh session fallback should depend on an empty transcript")
	}
}

func TestACPListSessionsPaginatesWithCWD(t *testing.T) {
	run := startFakeACP(t, map[string]string{"FAKEACP_LIST_PAGES": "3"}, nil)
	cwd := t.TempDir()
	sessions, err := run.backend.ListSessions(cwd, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 6 || sessions[0].ID != "s2-1" || sessions[5].ID != "s0-0" {
		t.Fatalf("sessions = %#v", sessions)
	}
	calls := fakeACPRequests(t, run.logPath, "session/list")
	if len(calls) != 3 {
		t.Fatalf("session/list calls = %d", len(calls))
	}
	for _, call := range calls {
		var params struct {
			CWD string `json:"cwd"`
		}
		_ = json.Unmarshal(call, &params)
		if params.CWD != cwd {
			t.Fatalf("session/list params = %s, want cwd %q", call, cwd)
		}
	}
	newest, err := run.backend.ListSessions(cwd, 1)
	if err != nil || len(newest) != 1 || newest[0].ID != "s2-1" {
		t.Fatalf("newest = %#v, %v", newest, err)
	}
}

// T-P2-06：message / thought chunk -> events。
func TestACPPromptStreamsMessageAndReasoning(t *testing.T) {
	run := startFakeACP(t, nil, nil)
	sid := newFakeSession(t, run)
	events := run.runTurn(t, sid, "happy")

	var kinds []string
	var text strings.Builder
	for _, event := range events {
		switch event := event.(type) {
		case EvTurnStarted:
			kinds = append(kinds, "started")
		case EvReasoningStarted:
			kinds = append(kinds, "rstart")
		case EvReasoningDelta:
			kinds = append(kinds, "rdelta")
		case EvReasoningCompleted:
			kinds = append(kinds, "rdone")
		case EvAssistantDelta:
			kinds = append(kinds, "delta")
			text.WriteString(event.Text)
		case EvTurnCompleted:
			kinds = append(kinds, "completed:"+string(event.Status))
		}
	}
	want := []string{"started", "rstart", "rdelta", "rdelta", "rdone", "delta", "delta", "completed:completed"}
	if !reflect.DeepEqual(kinds, want) || text.String() != "Hello world" {
		t.Fatalf("events = %v text=%q", kinds, text.String())
	}
}

// T-P2-07：tool_call / tool_call_update 合併，只含變動欄位的 update 不清掉先前欄位。
func TestACPToolLifecycle(t *testing.T) {
	run := startFakeACP(t, nil, nil)
	sid := newFakeSession(t, run)
	events := run.runTurn(t, sid, "tools")

	var started []EvToolStarted
	var outputs []string
	var completed []EvToolCompleted
	for _, event := range events {
		switch event := event.(type) {
		case EvToolStarted:
			started = append(started, event)
		case EvToolOutput:
			outputs = append(outputs, event.Text)
		case EvToolCompleted:
			completed = append(completed, event)
		}
	}
	if len(started) != 2 || started[0].ToolID != "t1" || started[0].Kind != ToolCommand || started[0].Title != "$ ls -la" ||
		started[1].Kind != ToolEdit || started[1].Title != "✎ Write x" {
		t.Fatalf("started = %#v", started)
	}
	if !reflect.DeepEqual(outputs, []string{"a.txt\n"}) {
		t.Fatalf("outputs = %q", outputs)
	}
	if len(completed) != 2 || completed[0].ToolID != "t1" || completed[0].Status != ToolOK || completed[0].Label != "" ||
		completed[1].ToolID != "t2" || completed[1].Status != ToolFailed || completed[1].Label != "failed" {
		t.Fatalf("completed = %#v", completed)
	}
}

// T-P2-08：plan / usage / commands / title / mode / 未知 update。
func TestACPMetaUpdates(t *testing.T) {
	run := startFakeACP(t, nil, nil)
	sid := newFakeSession(t, run)
	events := run.runTurn(t, sid, "meta")

	var plan *EvPlanUpdated
	var usages []EvUsage
	var commands *EvCommandsUpdated
	var title *EvSessionTitle
	var mode *EvModeWarning
	for _, event := range events {
		switch event := event.(type) {
		case EvPlanUpdated:
			plan = &event
		case EvUsage:
			usages = append(usages, event)
		case EvCommandsUpdated:
			commands = &event
		case EvSessionTitle:
			title = &event
		case EvModeWarning:
			mode = &event
		}
	}
	if plan == nil || len(plan.Items) != 1 || plan.Items[0] != (PlanItem{Text: "step one", Status: "in_progress"}) {
		t.Fatalf("plan = %#v", plan)
	}
	if len(usages) != 2 || usages[0].Used != 1200 || usages[0].Size == nil || *usages[0].Size != 200000 || usages[0].Cost != nil ||
		usages[1].Cost == nil || usages[1].Cost.Amount != 0.125 || usages[1].Cost.Currency != "USD" {
		t.Fatalf("usages = %#v", usages)
	}
	if commands == nil || len(commands.Commands) != 1 || commands.Commands[0].Name != "compact" {
		t.Fatalf("commands = %#v", commands)
	}
	if title == nil || title.Title != "Named by agent" {
		t.Fatalf("title = %#v", title)
	}
	if mode == nil || mode.Mode != "acceptEdits" || mode.Text != "permissions: acceptEdits (from agent settings)" {
		t.Fatalf("mode = %#v", mode)
	}
	if got := run.backend.unknownUpdateCount(); got != 3 {
		t.Fatalf("unknown updates = %d, want 3", got)
	}
	last := events[len(events)-1].(EvTurnCompleted)
	if last.Status != TurnCompleted {
		t.Fatalf("turn did not complete after unknown updates: %#v", last)
	}
}

// T-P2-22：session/new 的 mode 非 default 時送出警示；default 不送。
func TestACPModeWarningFromSessionNew(t *testing.T) {
	run := startFakeACP(t, map[string]string{"FAKEACP_MODE": "auto"}, nil)
	newFakeSession(t, run)
	warning, ok := nextEvent(t, run.events).(EvModeWarning)
	if !ok || warning.Text != "permissions: auto (from agent settings)" {
		t.Fatalf("warning = %#v", warning)
	}
}

func TestACPDefaultModeHasNoWarning(t *testing.T) {
	clean := startFakeACP(t, nil, nil)
	newFakeSession(t, clean)
	select {
	case event := <-clean.events:
		t.Fatalf("default mode produced event %#v", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestACPModeFromConfigOptions(t *testing.T) {
	options := []ConfigOption{{ID: "model", Current: "x"}, {ID: "mode", Category: "mode", Current: "bypassPermissions"}}
	if got := acpModeOf("", options); got != "bypassPermissions" {
		t.Fatalf("mode = %q", got)
	}
	if got := acpModeOf("plan", options); got != "plan" {
		t.Fatalf("modes.currentModeId should win: %q", got)
	}
}

func firstApproval(t *testing.T, events <-chan Event) EvApprovalRequest {
	t.Helper()
	collected := eventsUntil(t, events, func(event Event) bool { _, ok := event.(EvApprovalRequest); return ok })
	return collected[len(collected)-1].(EvApprovalRequest)
}

func lastMessage(events []Event) string {
	text := ""
	for _, event := range events {
		if delta, ok := event.(EvAssistantDelta); ok {
			text += delta.Text
		}
	}
	return text
}

// T-P2-09 / T-P2-20：approval options 取自 request，使用者明確選擇後才回 selected。
func TestACPPermissionSelected(t *testing.T) {
	run := startFakeACP(t, nil, nil)
	sid := newFakeSession(t, run)
	if _, err := run.backend.Prompt(sid, "perm", runtimeOverrides{}); err != nil {
		t.Fatal(err)
	}
	approval := firstApproval(t, run.events)
	if approval.ReqID != "0" || !approval.Explicit || approval.Title != "✎ Write /tmp/x" {
		t.Fatalf("approval = %#v", approval)
	}
	wantOptions := []ApprovalOption{
		{ID: "allow-once", Label: "Allow once", Kind: OptionAllowOnce},
		{ID: "allow-with-updates", Label: "Always allow", Kind: OptionAllowAlways},
		{ID: "reject", Label: "Reject", Kind: OptionRejectOnce},
	}
	if !reflect.DeepEqual(approval.Options, wantOptions) {
		t.Fatalf("options = %#v", approval.Options)
	}
	// 使用者還沒按鍵前，agent 不應收到任何 response。
	time.Sleep(100 * time.Millisecond)
	if got := responsesFor(t, run.logPath); len(got) != 0 {
		t.Fatalf("permission answered before user input: %v", got)
	}
	if err := run.backend.ResolveApproval(approval.ReqID, "allow-once"); err != nil {
		t.Fatal(err)
	}
	events := eventsUntil(t, run.events, isTurnCompleted)
	if got := lastMessage(events); got != "outcome:selected:allow-once" {
		t.Fatalf("agent saw %q", got)
	}
}

// responsesFor 回傳 agent 收到的 response（沒有 method 的訊息）的 result。
func responsesFor(t *testing.T, path string) []json.RawMessage {
	t.Helper()
	var out []json.RawMessage
	for _, entry := range readFakeACPLog(t, path) {
		raw, ok := entry["recv"]
		if !ok {
			continue
		}
		var message fakeACPMessage
		if json.Unmarshal(raw, &message) == nil && message.Method == "" && len(message.ID) > 0 {
			out = append(out, message.Result)
		}
	}
	return out
}

func TestACPPermissionUserCancelAndInvalidOption(t *testing.T) {
	run := startFakeACP(t, nil, nil)
	sid := newFakeSession(t, run)
	if _, err := run.backend.Prompt(sid, "perm", runtimeOverrides{}); err != nil {
		t.Fatal(err)
	}
	approval := firstApproval(t, run.events)
	if err := run.backend.ResolveApproval(approval.ReqID, CancelApprovalID); err != nil {
		t.Fatal(err)
	}
	if got := lastMessage(eventsUntil(t, run.events, isTurnCompleted)); got != "outcome:cancelled:" {
		t.Fatalf("agent saw %q", got)
	}

	if _, err := run.backend.Prompt(sid, "perm", runtimeOverrides{}); err != nil {
		t.Fatal(err)
	}
	approval = firstApproval(t, run.events)
	if err := run.backend.ResolveApproval(approval.ReqID, "made-up-option"); err == nil {
		t.Fatal("ResolveApproval accepted an option that is not in the request")
	}
	if got := lastMessage(eventsUntil(t, run.events, isTurnCompleted)); got != "outcome:cancelled:" {
		t.Fatalf("invalid option should fail closed, agent saw %q", got)
	}
}

// T-P2-20：non-interactive / timeout / sessionId 不符 / 無法 render -> cancelled。
func TestACPPermissionFailsClosed(t *testing.T) {
	t.Run("non-interactive", func(t *testing.T) {
		run := startFakeACP(t, nil, nil)
		sid := newFakeSession(t, run)
		_, _ = run.backend.Prompt(sid, "perm", runtimeOverrides{})
		approval := firstApproval(t, run.events)
		if err := run.backend.RejectRequest(approval.ReqID, "lumen requires an interactive terminal for approval"); err != nil {
			t.Fatal(err)
		}
		if got := lastMessage(eventsUntil(t, run.events, isTurnCompleted)); got != "outcome:cancelled:" {
			t.Fatalf("agent saw %q", got)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		run := startFakeACP(t, nil, func(cfg *acpConfig) { cfg.PermissionTimeout = 100 * time.Millisecond })
		sid := newFakeSession(t, run)
		_, _ = run.backend.Prompt(sid, "perm", runtimeOverrides{})
		events := eventsUntil(t, run.events, isTurnCompleted)
		if got := lastMessage(events); got != "outcome:cancelled:" {
			t.Fatalf("agent saw %q", got)
		}
		// 逾時要讓 UI 能清掉 approval card。
		expired := false
		for _, event := range events {
			if e, ok := event.(EvApprovalExpired); ok && e.ReqID == "0" {
				expired = true
			}
		}
		if !expired {
			t.Fatalf("timeout did not emit EvApprovalExpired: %#v", events)
		}
	})
	t.Run("session mismatch", func(t *testing.T) {
		run := startFakeACP(t, nil, nil)
		sid := newFakeSession(t, run)
		events := run.runTurn(t, sid, "permother")
		for _, event := range events {
			if _, ok := event.(EvApprovalRequest); ok {
				t.Fatal("mismatched session produced an approval card")
			}
		}
		if got := lastMessage(events); got != "outcome:cancelled:" {
			t.Fatalf("agent saw %q", got)
		}
	})
	t.Run("unrenderable", func(t *testing.T) {
		run := startFakeACP(t, nil, nil)
		sid := newFakeSession(t, run)
		events := run.runTurn(t, sid, "permempty")
		for _, event := range events {
			if _, ok := event.(EvApprovalRequest); ok {
				t.Fatal("request without options produced an approval card")
			}
		}
		if got := lastMessage(events); got != "outcome:cancelled:" {
			t.Fatalf("agent saw %q", got)
		}
	})
}

// T-P2-10：cancel 先回 pending permission 的 cancelled，再送 session/cancel；
// 未完成 tool 標 cancelled；cancel 後的 update 仍被接受；turn 以 interrupted 結束。
func TestACPCancelOrdersPermissionBeforeSessionCancel(t *testing.T) {
	run := startFakeACP(t, nil, nil)
	sid := newFakeSession(t, run)
	turnID, err := run.backend.Prompt(sid, "permcancel", runtimeOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	firstApproval(t, run.events)
	if err := run.backend.Cancel(sid, turnID); err != nil {
		t.Fatal(err)
	}
	events := eventsUntil(t, run.events, isTurnCompleted)
	if got := lastMessage(events); got != "outcome:cancelled:" {
		t.Fatalf("agent saw %q", got)
	}
	completed := events[len(events)-1].(EvTurnCompleted)
	if completed.Status != TurnInterrupted || completed.Label != "interrupted" {
		t.Fatalf("turn = %#v", completed)
	}
	var responseAt, cancelAt = -1, -1
	for index, entry := range readFakeACPLog(t, run.logPath) {
		raw, ok := entry["recv"]
		if !ok {
			continue
		}
		var message fakeACPMessage
		_ = json.Unmarshal(raw, &message)
		if message.Method == "" && len(message.ID) > 0 && responseAt < 0 {
			responseAt = index
		}
		if message.Method == "session/cancel" {
			cancelAt = index
		}
	}
	if responseAt < 0 || cancelAt < 0 || responseAt > cancelAt {
		t.Fatalf("order: permission response at %d, session/cancel at %d", responseAt, cancelAt)
	}
}

func TestACPCancelMarksToolsAndKeepsLateUpdates(t *testing.T) {
	run := startFakeACP(t, nil, nil)
	sid := newFakeSession(t, run)
	turnID, _ := run.backend.Prompt(sid, "slow", runtimeOverrides{})
	collected := eventsUntil(t, run.events, func(event Event) bool { _, ok := event.(EvToolStarted); return ok })
	if err := run.backend.Cancel(sid, turnID); err != nil {
		t.Fatal(err)
	}
	collected = append(collected, eventsUntil(t, run.events, isTurnCompleted)...)
	var cancelledTool, late bool
	for _, event := range collected {
		switch event := event.(type) {
		case EvToolCompleted:
			cancelledTool = event.ToolID == "slow1" && event.Status == ToolCancelled && event.Label == "cancelled"
		case EvAssistantDelta:
			late = late || event.Text == "late"
		}
	}
	if !cancelledTool || !late {
		t.Fatalf("cancelledTool=%v late=%v events=%#v", cancelledTool, late, collected)
	}
	if last := collected[len(collected)-1].(EvTurnCompleted); last.Status != TurnInterrupted {
		t.Fatalf("turn = %#v", last)
	}
}

func TestACPCancelTimeoutNotice(t *testing.T) {
	run := startFakeACP(t, map[string]string{"FAKEACP_IGNORE_CANCEL": "1"}, func(cfg *acpConfig) { cfg.CancelTimeout = 150 * time.Millisecond })
	sid := newFakeSession(t, run)
	turnID, _ := run.backend.Prompt(sid, "slow", runtimeOverrides{})
	eventsUntil(t, run.events, func(event Event) bool { _, ok := event.(EvToolStarted); return ok })
	_ = run.backend.Cancel(sid, turnID)
	events := eventsUntil(t, run.events, func(event Event) bool {
		notice, ok := event.(EvNotice)
		return ok && strings.Contains(notice.Text, "agent not responding to cancel")
	})
	if notice := events[len(events)-1].(EvNotice); notice.Level != NoticeWarn {
		t.Fatalf("notice = %#v", notice)
	}
}

// T-P2-12：subprocess 非預期結束 -> turn interrupted、EvConnectionLost（含 stderr 摘要）、Events 關閉。
func TestACPCrashReportsConnectionLost(t *testing.T) {
	run := startFakeACP(t, nil, nil)
	sid := newFakeSession(t, run)
	_, _ = run.backend.Prompt(sid, "crash", runtimeOverrides{})
	events := eventsUntil(t, run.events, func(event Event) bool { _, ok := event.(EvConnectionLost); return ok })
	lost := events[len(events)-1].(EvConnectionLost)
	if lost.Err == nil || !strings.Contains(lost.Err.Error(), "fatal: boom") || strings.Contains(lost.Err.Error(), "SEKRIT") {
		t.Fatalf("lost = %v", lost.Err)
	}
	var interrupted bool
	for _, event := range events {
		if completed, ok := event.(EvTurnCompleted); ok {
			interrupted = completed.Status == TurnInterrupted
		}
	}
	if !interrupted {
		t.Fatalf("in-flight turn was not marked interrupted: %#v", events)
	}
	select {
	case _, ok := <-run.events:
		if ok {
			t.Fatal("unexpected event after connection lost")
		}
	case <-time.After(time.Second):
		t.Fatal("events not closed after connection lost")
	}
	if disconnected, err := run.backend.Disconnected(); !disconnected || err == nil {
		t.Fatalf("Disconnected() = %v, %v", disconnected, err)
	}
}

// T-P2-12：supervisor 重連 -> load 接回，replay 以 history 回傳（不是 live event）；找不到 session 且 transcript 為空時開新 session。
func TestACPReconnectResumesViaLoad(t *testing.T) {
	cfg, _ := fakeACPConfig(t, nil)
	factory := func() (Backend, error) { return newACPBackend(cfg), nil }
	result := reconnectWithSnapshot(factory, reconnectSnapshot{threadID: "old", cwd: t.TempDir()}, nil)
	if result.err != nil || result.thread.ID != "old" || len(result.thread.Turns) != 2 {
		t.Fatalf("reconnect result = %#v", result)
	}
	if result.freshSession {
		t.Fatal("resumed session was reported as a fresh session")
	}
	result.backend.Close()
}

func TestACPReconnectFallsBackToNewSession(t *testing.T) {
	cfg, _ := fakeACPConfig(t, map[string]string{"FAKEACP_LOAD_ERROR": "1"})
	factory := func() (Backend, error) { return newACPBackend(cfg), nil }
	result := reconnectWithSnapshot(factory, reconnectSnapshot{threadID: "gone", cwd: t.TempDir()}, nil)
	if result.err != nil || result.thread.ID != "sess-new" {
		t.Fatalf("fresh session fallback = %#v", result)
	}
	if !result.freshSession {
		t.Fatal("new-session fallback was not flagged")
	}
	result.backend.Close()

	result = reconnectWithSnapshot(factory, reconnectSnapshot{threadID: "gone", cwd: t.TempDir(), hasTranscriptContent: true}, nil)
	if !errors.Is(result.err, errSessionNotFound) {
		t.Fatalf("with transcript the error must be kept: %#v", result)
	}
}

// T-P2-13：authMethods 非空或 session/new auth 錯誤 -> requires login，停在 disconnected。
func TestACPAuthRequired(t *testing.T) {
	cfg, _ := fakeACPConfig(t, map[string]string{"FAKEACP_AUTH": "1"})
	backend := newACPBackend(cfg)
	t.Cleanup(backend.Close)
	err := backend.Open(t.Context())
	if err == nil || !strings.Contains(err.Error(), "backend fake requires login") {
		t.Fatalf("Open() error = %v", err)
	}
	if disconnected, _ := backend.Disconnected(); !disconnected {
		t.Fatal("backend should stay disconnected")
	}
}

func TestACPNewSessionAuthError(t *testing.T) {
	run := startFakeACP(t, map[string]string{"FAKEACP_NEW_ERROR": "auth"}, nil)
	if _, err := run.backend.NewSession(t.TempDir(), runtimeOverrides{}); err == nil || !strings.Contains(err.Error(), "backend fake requires login") {
		t.Fatalf("NewSession() error = %v", err)
	}
}

// T-P2-14：stopReason 映射。
func TestACPStopReasonMapping(t *testing.T) {
	run := startFakeACP(t, nil, nil)
	sid := newFakeSession(t, run)
	cases := []struct {
		prompt string
		status TurnStatus
	}{
		{"stop:end_turn", TurnCompleted},
		{"stop:cancelled", TurnInterrupted},
		{"stop:refusal", TurnRefused},
		{"stop:max_tokens", TurnMaxTokens},
		{"stop:max_turn_requests", TurnMaxTokens},
		{"rpcerror", TurnFailed},
	}
	for _, tc := range cases {
		events := run.runTurn(t, sid, tc.prompt)
		completed := events[len(events)-1].(EvTurnCompleted)
		if completed.Status != tc.status {
			t.Errorf("%s: status = %q, want %q", tc.prompt, completed.Status, tc.status)
		}
		if tc.status == TurnFailed && (completed.Err == nil || !strings.Contains(completed.Err.Error(), "internal agent failure")) {
			t.Errorf("%s: err = %v", tc.prompt, completed.Err)
		}
	}
}

// T-P2-15：任何 error / status 輸出不含 args（假 secret）。
func TestACPErrorsDoNotLeakArgs(t *testing.T) {
	cfg, _ := fakeACPConfig(t, map[string]string{"FAKEACP_PROTOCOL": "9"})
	backend := newACPBackend(cfg)
	t.Cleanup(backend.Close)
	err := backend.Open(t.Context())
	if err == nil || strings.Contains(err.Error(), "SEKRIT") {
		t.Fatalf("Open() error = %v", err)
	}

	missing := cfg
	missing.Args = []string{"--token=SEKRIT-VALUE"}
	missing.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	other := newACPBackend(missing)
	err = other.Open(t.Context())
	if err == nil || strings.Contains(err.Error(), "SEKRIT") {
		t.Fatalf("not-found error leaks args: %v", err)
	}

	broken := cfg
	broken.LookPath = func(string) (string, error) { return filepath.Join(t.TempDir(), "does-not-exist"), nil }
	third := newACPBackend(broken)
	err = third.Open(t.Context())
	if err == nil || strings.Contains(err.Error(), "SEKRIT") {
		t.Fatalf("start error leaks args: %v", err)
	}
}

// T-P2-17：subprocess 的工作目錄是 Lumen 自有 state dir，專案路徑只經由 session/new.cwd。
func TestACPSubprocessRunsInStateDir(t *testing.T) {
	run := startFakeACP(t, nil, nil)
	project := t.TempDir()
	if _, err := run.backend.NewSession(project, runtimeOverrides{}); err != nil {
		t.Fatal(err)
	}
	var start struct {
		CWD string `json:"cwd"`
	}
	for _, entry := range readFakeACPLog(t, run.logPath) {
		if raw, ok := entry["start"]; ok {
			_ = json.Unmarshal(raw, &start)
		}
	}
	gotDir, _ := filepath.EvalSymlinks(start.CWD)
	wantDir, _ := filepath.EvalSymlinks(run.cfg.StateDir)
	projectDir, _ := filepath.EvalSymlinks(project)
	if gotDir != wantDir || gotDir == projectDir {
		t.Fatalf("agent cwd = %q, want state dir %q (project %q)", gotDir, wantDir, projectDir)
	}
	info, err := os.Stat(run.cfg.StateDir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("state dir = %v, %v", info, err)
	}
}

func TestLumenStateDirHonorsXDG(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	if dir, err := lumenStateDir(); err != nil || dir != "/xdg/state/lumen" {
		t.Fatalf("lumenStateDir() = %q, %v", dir, err)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/test")
	if dir, _ := lumenStateDir(); dir != "/home/test/.local/state/lumen" {
		t.Fatalf("lumenStateDir() = %q", dir)
	}
}

// T-P2-18：找不到預設 bin -> 顯示安裝指令、不自動下載、不呼叫 npx。
func TestACPMissingBinaryShowsInstallHint(t *testing.T) {
	cfg := claudeACPConfig()
	var looked []string
	cfg.LookPath = func(name string) (string, error) {
		looked = append(looked, name)
		return "", exec.ErrNotFound
	}
	cfg.StateDir = t.TempDir()
	backend := newACPBackend(cfg)
	err := backend.Open(t.Context())
	if err == nil || !strings.Contains(err.Error(), "npm i -g @agentclientprotocol/claude-agent-acp@0.90.0") {
		t.Fatalf("Open() error = %v", err)
	}
	if !reflect.DeepEqual(looked, []string{"claude-agent-acp"}) || strings.Contains(cfg.Command, "npx") {
		t.Fatalf("looked up %v", looked)
	}
	if disconnected, _ := backend.Disconnected(); !disconnected {
		t.Fatal("backend should stay disconnected")
	}
}

// T-P2-19：stderr 進有界 ring buffer；只在 connection lost 顯示最後 N 行。
func TestStderrRingIsBounded(t *testing.T) {
	ring := &stderrRing{}
	for i := 0; i < 5000; i++ {
		_, _ = ring.Write([]byte(strings.Repeat("y", 4000) + "\n"))
	}
	_, _ = ring.Write([]byte("last line without newline"))
	if ring.Len() > acpStderrMaxLines {
		t.Fatalf("ring holds %d lines", ring.Len())
	}
	tail := ring.Tail(3)
	if len(tail) != 3 || tail[2] != "last line without newline" || len(tail[0]) > acpStderrMaxLineLen {
		t.Fatalf("tail = %q", tail)
	}
}

func TestACPStderrStaysOutOfEventsAndIsSummarizedOnLoss(t *testing.T) {
	run := startFakeACP(t, map[string]string{"FAKEACP_STDERR_LINES": "3000"}, nil)
	sid := newFakeSession(t, run)
	events := run.runTurn(t, sid, "happy")
	for _, event := range events {
		if notice, ok := event.(EvNotice); ok {
			t.Fatalf("stderr leaked into a notice: %#v", notice)
		}
	}
	if run.backend.stderr.Len() > acpStderrMaxLines {
		t.Fatal("stderr ring is unbounded")
	}
	_, _ = run.backend.Prompt(sid, "crash", runtimeOverrides{})
	lost := eventsUntil(t, run.events, func(event Event) bool { _, ok := event.(EvConnectionLost); return ok })
	text := lost[len(lost)-1].(EvConnectionLost).Err.Error()
	if got := strings.Count(text, "stderr line"); got > acpStderrShowLines || !strings.Contains(text, "fatal: boom") {
		t.Fatalf("lost message shows %d stderr lines: %q", got, text)
	}
}

// T-P2-23：不送 mode 變更 request、不傳 bypass flag / env；env 中的 ANTHROPIC_* 與 CLAUDE_CODE_EXECUTABLE 被移除。
func TestACPNeverChangesPermissionModeOrLeaksEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-should-not-reach-agent")
	t.Setenv("CLAUDE_CODE_EXECUTABLE", "/bin/false")
	run := startFakeACP(t, nil, func(cfg *acpConfig) { cfg.SessionMeta = claudeACPConfig().SessionMeta })
	sid := newFakeSession(t, run)
	_, _ = run.backend.Prompt(sid, "perm", runtimeOverrides{})
	approval := firstApproval(t, run.events)
	_ = run.backend.ResolveApproval(approval.ReqID, "reject")
	eventsUntil(t, run.events, isTurnCompleted)
	_, _ = run.backend.Prompt(sid, "slow", runtimeOverrides{})
	eventsUntil(t, run.events, func(event Event) bool { _, ok := event.(EvToolStarted); return ok })
	_ = run.backend.Cancel(sid, "")
	eventsUntil(t, run.events, isTurnCompleted)

	for _, entry := range readFakeACPLog(t, run.logPath) {
		if raw, ok := entry["recv"]; ok {
			var message fakeACPMessage
			_ = json.Unmarshal(raw, &message)
			switch message.Method {
			case "session/set_mode", "session/set_config_option", "authenticate", "fs/write_text_file", "terminal/create":
				t.Fatalf("lumen sent %s", message.Method)
			}
		}
		if raw, ok := entry["start"]; ok {
			var start struct {
				Argv []string `json:"argv"`
				Env  []string `json:"env"`
			}
			_ = json.Unmarshal(raw, &start)
			if !reflect.DeepEqual(start.Argv[1:], run.cfg.Args) {
				t.Fatalf("argv = %v, want only the configured args %v", start.Argv[1:], run.cfg.Args)
			}
			env := strings.Join(start.Env, ",")
			if strings.Contains(env, "ANTHROPIC_") || strings.Contains(env, "CLAUDE_CODE_EXECUTABLE") {
				t.Fatalf("agent env contains scrubbed keys: %s", env)
			}
			for _, key := range []string{"HOME", "PATH"} {
				if !strings.Contains(env, key) {
					t.Fatalf("agent env lost %s", key)
				}
			}
		}
	}
	params := fakeACPRequests(t, run.logPath, "session/new")
	var meta struct {
		Meta struct {
			ClaudeCode struct {
				Options struct {
					Allow *bool `json:"allowDangerouslySkipPermissions"`
				} `json:"options"`
			} `json:"claudeCode"`
		} `json:"_meta"`
	}
	_ = json.Unmarshal(params[0], &meta)
	if meta.Meta.ClaudeCode.Options.Allow == nil || *meta.Meta.ClaudeCode.Options.Allow {
		t.Fatalf("session/new _meta = %s", params[0])
	}
}

func TestScrubACPEnv(t *testing.T) {
	got := scrubACPEnv([]string{"HOME=/h", "ANTHROPIC_API_KEY=k", "ANTHROPIC_BASE_URL=u", "CLAUDE_CODE_EXECUTABLE=/x", "CLAUDE_CONFIG_DIR=/c", "PATH=/bin", "NOEQUALS"})
	want := []string{"HOME=/h", "CLAUDE_CONFIG_DIR=/c", "PATH=/bin", "NOEQUALS"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scrubACPEnv() = %v", got)
	}
}

func acpMethods(t *testing.T, path string) []string {
	t.Helper()
	var methods []string
	for _, entry := range readFakeACPLog(t, path) {
		if raw, ok := entry["recv"]; ok {
			var message fakeACPMessage
			if json.Unmarshal(raw, &message) == nil && message.Method != "" {
				methods = append(methods, message.Method)
			}
		}
	}
	return methods
}

// T-P2-12：transcript 已有內容時 reconnect 優先 session/resume（不重播），沒有 resume 時 load 的 replay 被丟棄。
func TestACPReconnectWithTranscriptSkipsReplay(t *testing.T) {
	for name, tc := range map[string]struct {
		caps, want string
	}{"resume": {"load,resume,list", "session/resume"}, "load only": {"load,list", "session/load"}} {
		t.Run(name, func(t *testing.T) {
			cfg, logPath := fakeACPConfig(t, map[string]string{"FAKEACP_CAPS": tc.caps})
			factory := func() (Backend, error) { return newACPBackend(cfg), nil }
			result := reconnectWithSnapshot(factory, reconnectSnapshot{threadID: "old", cwd: t.TempDir(), hasTranscriptContent: true}, nil)
			if result.err != nil || result.thread.ID != "old" || len(result.thread.Turns) != 0 {
				t.Fatalf("reconnect result = %#v", result)
			}
			result.backend.Close()
			if methods := strings.Join(acpMethods(t, logPath), ","); !strings.Contains(methods, tc.want) {
				t.Fatalf("methods = %s, want %s", methods, tc.want)
			}
		})
	}
}

// 沒有 transcript（啟動 --resume 後馬上斷線）時仍用 load 取回 history。
func TestACPReconnectWithoutTranscriptStillLoads(t *testing.T) {
	cfg, logPath := fakeACPConfig(t, map[string]string{"FAKEACP_CAPS": "load,resume,list"})
	factory := func() (Backend, error) { return newACPBackend(cfg), nil }
	result := reconnectWithSnapshot(factory, reconnectSnapshot{threadID: "old", cwd: t.TempDir()}, nil)
	if result.err != nil || len(result.thread.Turns) != 2 {
		t.Fatalf("reconnect result = %#v", result)
	}
	result.backend.Close()
	if methods := strings.Join(acpMethods(t, logPath), ","); !strings.Contains(methods, "session/load") {
		t.Fatalf("methods = %s", methods)
	}
}
