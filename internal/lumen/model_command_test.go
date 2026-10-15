package lumen

import (
	"errors"
	"strings"
	"testing"
)

// modelBackend 是同時支援 ModelPicker / ModelSetter 的 ACP-like backend。
type modelBackend struct {
	swapBackend
	options  []modelOption
	listCall int
	setIDs   []string
	setErr   error
}

func (b *modelBackend) ModelOptions() ([]modelOption, error) {
	b.listCall++
	return b.options, nil
}

func (b *modelBackend) SetModel(_, id string) (string, error) {
	b.setIDs = append(b.setIDs, id)
	if b.setErr != nil {
		return "", b.setErr
	}
	return id, nil
}

// pickerOnlyBackend 模擬 codex：只能列出，不能 session 內切換。
type pickerOnlyBackend struct {
	swapBackend
	options []modelOption
}

func (b *pickerOnlyBackend) ModelOptions() ([]modelOption, error) { return b.options, nil }

func newModelBackend() *modelBackend {
	backend := &modelBackend{
		swapBackend: *acpLikeBackend("claude", Capabilities{ModelList: true, ModelSwitch: true}),
		options: []modelOption{
			{ID: "default", Name: "Default (recommended)", Description: "Opus 5.5"},
			{ID: "opus", Name: "Opus 5.5", Current: true},
			{ID: "sonnet", Name: "Sonnet 5.5"},
		},
	}
	return backend
}

func typeInput(frontend *ui, value string) {
	frontend.input, frontend.cursor = []rune(value), len([]rune(value))
	frontend.markInputEdited()
}

func TestModelCommandListsAcpModelsWithCurrentMarked(t *testing.T) {
	backend := newModelBackend()
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend, thread: threadSummary{ID: "s1", Model: "opus"}}
	for _, command := range []string{"/model", "/model list"} {
		if err := frontend.handleCommand(command); err != nil {
			t.Fatalf("%s: %v", command, err)
		}
	}
	plain := stripANSI(frontend.screenFrame(120, 30))
	for _, want := range []string{"* opus · Opus 5.5", "  sonnet · Sonnet 5.5", "  default · Default (recommended) · Opus 5.5"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("model list missing %q: %q", want, plain)
		}
	}
}

func TestModelCommandSwitchesAcpModel(t *testing.T) {
	backend := newModelBackend()
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend, thread: threadSummary{ID: "s1", Model: "opus"}}
	if err := frontend.handleCommand("/model sonnet"); err != nil {
		t.Fatal(err)
	}
	if len(backend.setIDs) != 1 || backend.setIDs[0] != "sonnet" {
		t.Fatalf("SetModel calls = %v", backend.setIDs)
	}
	if frontend.thread.Model != "sonnet" || frontend.effectiveModel() != "sonnet" || frontend.overrides.model != "" {
		t.Fatalf("model = %q override = %q", frontend.thread.Model, frontend.overrides.model)
	}
}

func TestModelCommandSwitchErrorKeepsModel(t *testing.T) {
	backend := newModelBackend()
	backend.setErr = errors.New(`unknown model "x"; available: default, opus, sonnet`)
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend, thread: threadSummary{ID: "s1", Model: "opus"}}
	err := frontend.handleCommand("/model x")
	if err == nil || !strings.Contains(err.Error(), "available: default, opus, sonnet") {
		t.Fatalf("error = %v", err)
	}
	if frontend.thread.Model != "opus" {
		t.Fatalf("model changed to %q", frontend.thread.Model)
	}
}

// codex 類 backend（只有 ModelPicker）：/model <id> 仍是 per-turn override，不呼叫 SetModel。
func TestModelCommandKeepsOverrideWithoutSetter(t *testing.T) {
	backend := &pickerOnlyBackend{swapBackend: swapBackend{name: "codex", log: &[]string{}, caps: Capabilities{ModelList: true, ModelSwitch: true}}}
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend, thread: threadSummary{ID: "s1", Model: "gpt-5"}}
	if err := frontend.handleCommand("/model gpt-6"); err != nil {
		t.Fatal(err)
	}
	if frontend.overrides.model != "gpt-6" {
		t.Fatalf("override = %q", frontend.overrides.model)
	}
}

func TestModelCommandAvailableInDynamicCatalogOnlyWithModelList(t *testing.T) {
	with := &ui{screenMode: true, backend: newModelBackend()}
	if !contains(catalogNames(with.filterSlashCommands("")), "model") {
		t.Fatal("/model missing from ACP catalog despite ModelList")
	}
	without := &ui{screenMode: true, backend: acpLikeBackend("claude", Capabilities{})}
	if contains(catalogNames(without.filterSlashCommands("")), "model") {
		t.Fatal("/model present without ModelList")
	}
}

func TestEvModelChangedUpdatesDisplayedModel(t *testing.T) {
	frontend := &ui{screenMode: true, backend: newModelBackend(), thread: threadSummary{Model: "opus"}}
	frontend.handleEvent(EvModelChanged{Model: "sonnet"})
	if frontend.effectiveModel() != "sonnet" {
		t.Fatalf("model = %q", frontend.effectiveModel())
	}
}

func TestModelArgPopupCompletesWithTab(t *testing.T) {
	backend := newModelBackend()
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend, thread: threadSummary{ID: "s1", Model: "opus"}}

	typeInput(frontend, "/model ")
	if !frontend.commandPopupVisible() {
		t.Fatal("popup hidden after `/model `")
	}
	if got := strings.Join(catalogNames(frontend.commandPopupItems()), ","); got != "default,opus,sonnet" {
		t.Fatalf("items = %s", got)
	}
	plain := stripANSI(frontend.screenFrame(120, 30))
	for _, want := range []string{"models", "sonnet", "Sonnet 5.5", "(current)", "Tab complete"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("popup missing %q: %q", want, plain)
		}
	}

	typeInput(frontend, "/model son")
	if got := strings.Join(catalogNames(frontend.commandPopupItems()), ","); got != "sonnet" {
		t.Fatalf("filtered items = %s", got)
	}
	if err := frontend.handleKey(keyEvent{typ: keyTab}); err != nil {
		t.Fatal(err)
	}
	if string(frontend.input) != "/model sonnet" {
		t.Fatalf("input after Tab = %q", string(frontend.input))
	}
	if len(backend.setIDs) != 0 {
		t.Fatal("Tab ran the command")
	}

	// 已是完整 ID：Enter 直接執行。
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatal(err)
	}
	if len(backend.setIDs) != 1 || backend.setIDs[0] != "sonnet" {
		t.Fatalf("SetModel calls = %v", backend.setIDs)
	}
}

func TestModelArgPopupEnterWithPartialCompletesThenRuns(t *testing.T) {
	backend := newModelBackend()
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend, thread: threadSummary{ID: "s1", Model: "opus"}}
	typeInput(frontend, "/model son")
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatal(err)
	}
	if len(backend.setIDs) != 1 || backend.setIDs[0] != "sonnet" {
		t.Fatalf("SetModel calls = %v", backend.setIDs)
	}
}

func TestModelArgPopupEnterWithoutPartialLists(t *testing.T) {
	backend := newModelBackend()
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend, thread: threadSummary{ID: "s1", Model: "opus"}}
	typeInput(frontend, "/model ")
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatal(err)
	}
	if len(backend.setIDs) != 0 {
		t.Fatalf("bare /model switched models: %v", backend.setIDs)
	}
}

func TestModelArgPopupCachesOptionsAndOnlyAppliesToModel(t *testing.T) {
	backend := newModelBackend()
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend, thread: threadSummary{ID: "s1", Model: "opus"}}
	for _, value := range []string{"/model ", "/model s", "/model so"} {
		typeInput(frontend, value)
		frontend.commandPopupVisible()
		frontend.commandPopupItems()
	}
	if backend.listCall != 1 {
		t.Fatalf("ModelOptions called %d times for one session", backend.listCall)
	}
	typeInput(frontend, "/permissions ")
	if frontend.commandPopupVisible() {
		t.Fatal("argument popup leaked to /permissions")
	}
	frontend.thread.ID = "s2"
	typeInput(frontend, "/model ")
	frontend.commandPopupItems()
	if backend.listCall != 2 {
		t.Fatalf("cache not invalidated on session change: %d", backend.listCall)
	}
}

func TestModelArgPopupHiddenWithoutModelList(t *testing.T) {
	frontend := &ui{screenMode: true, backend: acpLikeBackend("claude", Capabilities{})}
	typeInput(frontend, "/model ")
	if frontend.commandPopupVisible() {
		t.Fatal("popup visible without ModelList")
	}
}
