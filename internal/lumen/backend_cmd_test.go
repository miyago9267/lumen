package lumen

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// swapBackend 是可記錄呼叫順序、可注入失敗的假 backend。
type swapBackend struct {
	fakeBackend
	name    string
	log     *[]string
	openErr error
	newErr  error
	caps    Capabilities
}

func (b *swapBackend) Name() string       { return b.name }
func (b *swapBackend) Caps() Capabilities { return b.caps }
func (b *swapBackend) Close()             { *b.log = append(*b.log, "close "+b.name) }
func (b *swapBackend) Open(context.Context) error {
	*b.log = append(*b.log, "open "+b.name)
	return b.openErr
}
func (b *swapBackend) NewSession(cwd string, _ runtimeOverrides) (SessionInfo, error) {
	*b.log = append(*b.log, "new "+b.name)
	if b.newErr != nil {
		return SessionInfo{}, b.newErr
	}
	return SessionInfo{ID: b.name + "-session", CWD: cwd}, nil
}

type switchHarness struct {
	ui       *ui
	old, new *swapBackend
	log      *[]string
	built    []string
}

func newSwitchHarness(t *testing.T) *switchHarness {
	t.Helper()
	log := &[]string{}
	h := &switchHarness{
		log: log,
		old: &swapBackend{name: "codex", log: log},
		new: &swapBackend{name: "claude", log: log},
	}
	h.ui = &ui{
		screenMode: true,
		backend:    h.old,
		thread:     threadSummary{ID: "codex-session"},
		cwd:        t.TempDir(),
		registry:   newBuiltinRegistry(),
		overrides:  runtimeOverrides{model: "gpt-x"},
		openBackend: func(name string) (Backend, error) {
			h.built = append(h.built, name)
			if name == "claude" {
				return h.new, nil
			}
			return &swapBackend{name: name, log: log}, nil
		},
	}
	return h
}

func (h *switchHarness) frame() string { return stripANSI(h.ui.screenFrame(120, 30)) }

// T-P3-06：idle 時先 Open + NewSession 新 backend，成功後才關閉舊 backend；transcript 保留並插入分隔列。
func TestBackendSwitchOpensNewBeforeClosingOld(t *testing.T) {
	h := newSwitchHarness(t)
	h.ui.screenAdd(screenUser, "old prompt")
	h.ui.agentCommands = []SlashCommand{{Name: "stale"}}
	h.ui.permissionNote = "permissions: auto (from agent settings)"
	if err := h.ui.handleCommand("/backend claude"); err != nil {
		t.Fatalf("handleCommand error = %v", err)
	}
	if got := strings.Join(*h.log, ","); got != "open claude,new claude,close codex" {
		t.Fatalf("call order = %q", got)
	}
	if h.ui.backend != Backend(h.new) || h.ui.thread.ID != "claude-session" {
		t.Fatalf("backend/thread not swapped: %v %q", h.ui.backend, h.ui.thread.ID)
	}
	frame := h.frame()
	if !strings.Contains(frame, "old prompt") {
		t.Fatalf("old transcript was dropped: %q", frame)
	}
	if !strings.Contains(frame, "backend: codex → claude · new session") {
		t.Fatalf("separator row missing: %q", frame)
	}
	if h.ui.overrides != (runtimeOverrides{}) {
		t.Fatalf("codex runtime overrides leaked to the new backend: %#v", h.ui.overrides)
	}
	if len(h.ui.agentCommands) != 0 || h.ui.permissionNote != "" {
		t.Fatalf("old backend state leaked: %v %q", h.ui.agentCommands, h.ui.permissionNote)
	}
	// 之後 reconnect 要重建的是新 backend。
	if factory := h.ui.serverFactory; factory == nil {
		t.Fatal("serverFactory not updated")
	}
}

// T-P3-07：新 backend 啟動或 NewSession 失敗 -> 保留舊 backend 與 session，顯示錯誤。
func TestBackendSwitchFailureKeepsOldBackend(t *testing.T) {
	for name, tweak := range map[string]func(*switchHarness){
		"open": func(h *switchHarness) {
			h.new.openErr = errors.New("backend claude: claude-agent-acp not found · install: npm i -g x")
		},
		"session": func(h *switchHarness) { h.new.newErr = errors.New("session boom") },
	} {
		h := newSwitchHarness(t)
		tweak(h)
		if err := h.ui.handleCommand("/backend claude"); err != nil {
			t.Fatalf("%s: handleCommand error = %v", name, err)
		}
		if h.ui.backend != Backend(h.old) || h.ui.thread.ID != "codex-session" {
			t.Fatalf("%s: old backend/session not preserved", name)
		}
		for _, entry := range *h.log {
			if entry == "close codex" {
				t.Fatalf("%s: old backend was closed: %v", name, *h.log)
			}
		}
		if (*h.log)[len(*h.log)-1] != "close claude" {
			t.Fatalf("%s: failed new backend not closed: %v", name, *h.log)
		}
		if h.ui.overrides.model != "gpt-x" {
			t.Fatalf("%s: overrides were reset on failed switch", name)
		}
		if frame := h.frame(); !strings.Contains(frame, "switch to claude failed") {
			t.Fatalf("%s: no error shown: %q", name, frame)
		}
	}
}

// T-P3-08：busy / pending approval / pending input / queued prompt / reconnecting -> 拒絕並提示先 interrupt。
func TestBackendSwitchRefusedWhenNotIdle(t *testing.T) {
	cases := map[string]func(*switchHarness){
		"busy":     func(h *switchHarness) { h.ui.busy = true },
		"approval": func(h *switchHarness) { h.ui.approval = &approvalRequest{id: "1"} },
		"input":    func(h *switchHarness) { h.ui.inputRequest = &userInputRequest{id: "1"} },
		"queued":   func(h *switchHarness) { h.ui.queuedPrompts = []string{"next"} },
		"detached": func(h *switchHarness) { h.ui.backend = nil },
	}
	for name, setup := range cases {
		h := newSwitchHarness(t)
		setup(h)
		if err := h.ui.handleCommand("/backend claude"); err != nil {
			t.Fatalf("%s: handleCommand error = %v", name, err)
		}
		if len(h.built) != 0 || len(*h.log) != 0 {
			t.Fatalf("%s: backend was started or closed: %v %v", name, h.built, *h.log)
		}
		if name != "detached" && h.ui.backend != Backend(h.old) {
			t.Fatalf("%s: backend changed", name)
		}
		if name == "detached" && h.ui.backend != nil {
			t.Fatalf("%s: backend changed", name)
		}
		if frame := h.frame(); !strings.Contains(frame, "/backend") || (name != "detached" && !strings.Contains(frame, "interrupt")) {
			t.Fatalf("%s: refusal hint missing: %q", name, frame)
		}
	}
}

func TestBackendSwitchUnknownAndSameName(t *testing.T) {
	h := newSwitchHarness(t)
	if err := h.ui.handleCommand("/backend nope"); err != nil {
		t.Fatal(err)
	}
	if frame := h.frame(); !strings.Contains(frame, `unknown backend "nope"`) || !strings.Contains(frame, "claude, gemini, codex") {
		t.Fatalf("unknown backend message: %q", frame)
	}
	if err := h.ui.handleCommand("/backend codex"); err != nil {
		t.Fatal(err)
	}
	if frame := h.frame(); !strings.Contains(frame, "already using backend codex") {
		t.Fatalf("same-backend message: %q", frame)
	}
	if len(h.built) != 0 || len(*h.log) != 0 {
		t.Fatalf("no-op switch touched backends: %v %v", h.built, *h.log)
	}
}

// /backend 無參數：picker 列出所有 backend、標記目前者；上下鍵 + Enter 切換，Esc 關閉。
func TestBackendPicker(t *testing.T) {
	h := newSwitchHarness(t)
	if err := h.ui.handleCommand("/backend"); err != nil {
		t.Fatal(err)
	}
	frame := h.frame()
	for _, want := range []string{"claude", "gemini", "codex", "current"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("picker missing %q: %q", want, frame)
		}
	}
	if err := h.ui.handleKey(keyEvent{typ: keyEscape}); err != nil || h.ui.backendPicker {
		t.Fatalf("Esc did not close the picker: %v", err)
	}
	if len(*h.log) != 0 {
		t.Fatalf("closing the picker switched backends: %v", *h.log)
	}

	_ = h.ui.handleCommand("/backend")
	// 目前是 codex（index 2）；選到 claude（index 0）。
	for _, key := range []keyEvent{{typ: keyUp}, {typ: keyUp}, {typ: keyEnter}} {
		if err := h.ui.handleKey(key); err != nil {
			t.Fatal(err)
		}
	}
	if h.ui.backendPicker || h.ui.backend != Backend(h.new) {
		t.Fatalf("picker selection did not switch: picker=%v backend=%v", h.ui.backendPicker, h.ui.backend)
	}

	// 數字鍵直接選。
	h2 := newSwitchHarness(t)
	_ = h2.ui.handleCommand("/backend")
	_ = h2.ui.handleKey(keyEvent{typ: keyRune, rune: '1'})
	if h2.ui.backend != Backend(h2.new) {
		t.Fatal("digit key did not switch")
	}
}

func TestBackendPickerRefusedWhenBusy(t *testing.T) {
	h := newSwitchHarness(t)
	h.ui.busy = true
	_ = h.ui.handleCommand("/backend")
	if h.ui.backendPicker {
		t.Fatal("picker opened while busy")
	}
	if frame := h.frame(); !strings.Contains(frame, "interrupt") {
		t.Fatalf("no interrupt hint: %q", frame)
	}
}

// /backend 在 catalog 中，且 Lumen-local 永遠可用。
func TestBackendCommandInCatalog(t *testing.T) {
	if _, ok := slashCommandByName("backend"); !ok {
		t.Fatal("/backend missing from slash catalog")
	}
}

// 切換後 reconnect supervisor 重建的是目前的 backend，不是啟動時的那個。
func TestSupervisorFactoryFollowsBackendSwitch(t *testing.T) {
	h := newSwitchHarness(t)
	started := ""
	h.ui.serverFactory = func() (Backend, error) { started = "old"; return nil, errors.New("old factory") }
	if err := h.ui.handleCommand("/backend claude"); err != nil {
		t.Fatal(err)
	}
	supervisor := h.ui.newSupervisor()
	t.Cleanup(supervisor.Close)
	supervisor.Start(reconnectSnapshot{cwd: h.ui.cwd})
	<-supervisor.Results()
	if started != "" {
		t.Fatalf("supervisor used the pre-switch factory")
	}
	if got := strings.Join(h.built, ","); got != "claude,claude" {
		t.Fatalf("built backends = %q, want the reconnect to rebuild claude", got)
	}
}
