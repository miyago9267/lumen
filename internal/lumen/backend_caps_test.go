package lumen

import (
	"errors"
	"strings"
	"testing"
)

func acpLikeBackend(name string, caps Capabilities) *swapBackend {
	caps.SlashCatalog = "dynamic"
	return &swapBackend{name: name, log: &[]string{}, caps: caps}
}

func catalogNames(commands []slashCommand) []string {
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		names = append(names, command.name)
	}
	return names
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func turnCardRows(frontend *ui) (plain string, hasForkAction bool) {
	frontend.beginScreenTurn("turn-1")
	frontend.screenAdd(screenUser, "question")
	frontend.screenAdd(screenAssistant, "answer")
	frontend.endScreenTurn()
	rows := frontend.screenTranscriptRows(80)
	values := make([]string, 0, len(rows))
	for _, row := range rows {
		values = append(values, row.rendered+" "+row.tooltip)
		for _, action := range row.actions {
			hasForkAction = hasForkAction || action.action == screenTurnActionFork
		}
	}
	return stripANSI(strings.Join(values, "\n")), hasForkAction
}

// T-P3-09：ForkAtTurn=false 時不渲染 [fork]（也不留 tooltip 提示）；true 時才渲染。
func TestForkButtonRequiresCapability(t *testing.T) {
	off := &ui{screenMode: true, cwd: "/tmp", backend: acpLikeBackend("claude", Capabilities{})}
	plain, fork := turnCardRows(off)
	if fork || strings.Contains(plain, "[fork]") {
		t.Fatalf("[fork] rendered without ForkAtTurn: %q", plain)
	}
	if !strings.Contains(plain, "[ask]") {
		t.Fatalf("[ask] missing: %q", plain)
	}
	on := &ui{screenMode: true, cwd: "/tmp", backend: acpLikeBackend("codex", Capabilities{ForkAtTurn: true})}
	plain, fork = turnCardRows(on)
	if !fork || !strings.Contains(plain, "[fork]") {
		t.Fatalf("[fork] missing with ForkAtTurn: %q", plain)
	}
}

// T-P3-09：capability 為 false 的 /goal /review /compact 顯示 fallback，且不當成 prompt 送出。
func TestCapabilityFallbackOnDynamicBackend(t *testing.T) {
	backend := acpLikeBackend("claude", Capabilities{})
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend, overrides: runtimeOverrides{}}
	for _, command := range []string{"/goal", "/review", "/compact", "/ide", "/model gpt-5", "/permissions never", "/skills"} {
		if err := frontend.handleCommand(command); err != nil {
			t.Fatalf("handleCommand(%q) error = %v", command, err)
		}
		expected := strings.Fields(command)[0] + " is not supported by backend claude"
		if plain := stripANSI(frontend.screenFrame(120, 30)); !strings.Contains(plain, expected) {
			t.Fatalf("%q: no fallback %q in %q", command, expected, plain)
		}
	}
	if len(backend.prompts) != 0 {
		t.Fatalf("slash text was sent as prompt: %q", backend.prompts)
	}
	if frontend.overrides != (runtimeOverrides{}) {
		t.Fatalf("codex-only override was recorded on a non-codex backend: %#v", frontend.overrides)
	}
	if err := frontend.handleCommand("/definitely-unknown"); err == nil {
		t.Fatal("unknown command did not error")
	}
}

// T-P3-10：ACP backend 的 popup 顯示 Lumen-local 與 agent 的 availableCommands；執行 agent command 以 "/name args" 送 prompt。
func TestDynamicSlashCatalogAndAgentCommands(t *testing.T) {
	backend := acpLikeBackend("claude", Capabilities{})
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend}
	frontend.handleEvent(EvCommandsUpdated{Commands: []SlashCommand{
		{Name: "init", Description: "create CLAUDE.md"},
		{Name: "/compact", Description: "compact context"},
		{Name: "status", Description: "agent status (shadowed by local)"},
	}})

	names := catalogNames(frontend.filterSlashCommands(""))
	for _, want := range []string{"backend", "help", "status", "copy", "diff", "pwd", "init", "compact"} {
		if !contains(names, want) {
			t.Errorf("catalog missing %q: %v", want, names)
		}
	}
	for _, not := range []string{"goal", "ide", "review", "fork", "mcp", "model"} {
		if contains(names, not) {
			t.Errorf("codex-only %q leaked into the dynamic catalog: %v", not, names)
		}
	}
	count := 0
	for _, name := range names {
		if name == "status" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("status appears %d times; Lumen-local must shadow the agent command", count)
	}
	if got := catalogNames(frontend.filterSlashCommands("co")); !contains(got, "compact") || !contains(got, "copy") {
		t.Errorf("filter co = %v", got)
	}

	// popup 與 Enter 補全走同一份 catalog。
	frontend.input, frontend.cursor = []rune("/in"), 3
	if !frontend.commandPopupVisible() {
		t.Fatal("popup not visible for agent command prefix")
	}
	if items := catalogNames(frontend.commandPopupItems()); len(items) != 1 || items[0] != "init" {
		t.Fatalf("popup items = %v", items)
	}
	frontend.input, frontend.cursor = nil, 0

	if err := frontend.handleCommand("/init  --force"); err != nil {
		t.Fatal(err)
	}
	if len(backend.prompts) != 1 || backend.prompts[0] != "/init  --force" {
		t.Fatalf("agent command prompt = %q", backend.prompts)
	}
	frontend.busy = false
	if err := frontend.handleCommand("/compact"); err != nil {
		t.Fatal(err)
	}
	if backend.prompts[len(backend.prompts)-1] != "/compact" {
		t.Fatalf("prompts = %q", backend.prompts)
	}

	// Lumen-local 不送給 agent。
	frontend.busy = false
	before := len(backend.prompts)
	if err := frontend.handleCommand("/pwd"); err != nil {
		t.Fatal(err)
	}
	if len(backend.prompts) != before {
		t.Fatal("local command was sent to the agent")
	}
	// /help 列出目前 catalog。
	if err := frontend.handleCommand("/help"); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(frontend.screenFrame(120, 40))
	if !strings.Contains(plain, "/init") || strings.Contains(plain, "/goal") {
		t.Fatalf("help text = %q", plain)
	}
}

// busy 時 agent command 進 queue，不得直接送出第二個 prompt。
func TestAgentCommandQueuesWhileBusy(t *testing.T) {
	backend := acpLikeBackend("claude", Capabilities{})
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend, busy: true}
	frontend.handleEvent(EvCommandsUpdated{Commands: []SlashCommand{{Name: "init"}}})
	frontend.input, frontend.cursor = []rune("/init"), 5
	if err := frontend.handleCommand("/init"); err != nil {
		t.Fatal(err)
	}
	if len(backend.prompts) != 0 || len(frontend.queuedPrompts) != 1 || frontend.queuedPrompts[0] != "/init" {
		t.Fatalf("prompts=%q queued=%q", backend.prompts, frontend.queuedPrompts)
	}
}

// 靜態 catalog（codex）維持原樣：codex 專屬指令在 catalog，/backend 也在。
func TestStaticCatalogUnchangedForCodexBackend(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: &swapBackend{name: "codex", log: &[]string{}}}
	names := catalogNames(frontend.filterSlashCommands(""))
	for _, want := range []string{"goal", "ide", "backend", "dashboard"} {
		if !contains(names, want) {
			t.Errorf("static catalog missing %q", want)
		}
	}
}

// 切換 backend 後 catalog 即時重算。
func TestCatalogRecomputedAfterBackendSwitch(t *testing.T) {
	h := newSwitchHarness(t)
	h.new.caps = Capabilities{SlashCatalog: "dynamic"}
	if !contains(catalogNames(h.ui.filterSlashCommands("")), "goal") {
		t.Fatal("codex catalog should include /goal")
	}
	if err := h.ui.handleCommand("/backend claude"); err != nil {
		t.Fatal(err)
	}
	if contains(catalogNames(h.ui.filterSlashCommands("")), "goal") {
		t.Fatal("/goal still offered after switching to a dynamic backend")
	}
}

// Dashboard / resume picker：沒有 SessionList / SessionResume 的 backend 顯示 capability fallback。
func TestSessionPickersFallBackWithoutCapabilities(t *testing.T) {
	backend := acpLikeBackend("claude", Capabilities{})
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend, thread: threadSummary{ID: "cur"}}
	if err := frontend.openDashboard(); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(frontend.screenFrame(120, 30))
	if !strings.Contains(plain, "listing sessions is not supported by backend claude") {
		t.Fatalf("dashboard fallback missing: %q", plain)
	}
	if strings.Contains(plain, "Codex") {
		t.Fatalf("dashboard still says Codex on another backend: %q", plain)
	}
	frontend.dashboard = false
	if err := frontend.handleCommand("/resume abc"); err != nil {
		t.Fatal(err)
	}
	if plain := stripANSI(frontend.screenFrame(120, 30)); !strings.Contains(plain, "/resume is not supported by backend claude") {
		t.Fatalf("resume fallback missing: %q", plain)
	}

	// 有 list 但沒有 resume：可以看列表，attach 顯示 fallback。
	backend = acpLikeBackend("claude", Capabilities{SessionList: true})
	frontend = &ui{screenMode: true, cwd: "/tmp", backend: backend, thread: threadSummary{ID: "cur"}}
	_ = frontend.openDashboard()
	frontend.dashboardRows = []threadSummary{{ID: "cur"}, {ID: "other"}}
	_ = frontend.attachDashboard(1)
	if !strings.Contains(frontend.dashboardError, "not supported by backend claude") {
		t.Fatalf("attach fallback = %q", frontend.dashboardError)
	}
}

// T-P3-09：重連中（backend nil）沿用最後已知的 capability；從未知到過就不渲染 [fork]。
func TestForkButtonHiddenWhileReconnectingOnACP(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: acpLikeBackend("claude", Capabilities{})}
	frontend.markConnectionLost(errors.New("killed"))
	plain, fork := turnCardRows(frontend)
	if fork || strings.Contains(plain, "[fork]") {
		t.Fatalf("[fork] rendered while reconnecting on a backend without ForkAtTurn: %q", plain)
	}
	unknown := &ui{screenMode: true, cwd: "/tmp"}
	if plain, fork = turnCardRows(unknown); fork || strings.Contains(plain, "[fork]") {
		t.Fatalf("[fork] rendered with unknown capabilities: %q", plain)
	}
	codex := &ui{screenMode: true, cwd: "/tmp", backend: acpLikeBackend("codex", Capabilities{ForkAtTurn: true})}
	codex.markConnectionLost(errors.New("killed"))
	if plain, fork = turnCardRows(codex); !fork || !strings.Contains(plain, "[fork]") {
		t.Fatalf("[fork] lost for codex while reconnecting: %q", plain)
	}
}

// session_start 事件列顯示目前 backend 名稱；codex 維持 [app-server]。
func TestSessionStartLabelFollowsBackend(t *testing.T) {
	for name, want := range map[string]string{"claude": "session_start  [claude]", "codex": "session_start  [app-server]"} {
		frontend := &ui{backend: acpLikeBackend(name, Capabilities{})}
		if got := frontend.sessionStartLabel(); got != want {
			t.Fatalf("%s label = %q, want %q", name, got, want)
		}
	}
	if got := (&ui{}).sessionStartLabel(); got != "session_start  [app-server]" {
		t.Fatalf("nil backend label = %q", got)
	}
}
