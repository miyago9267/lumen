package lumen

import (
	"strings"
	"testing"
)

// pickerOnlyBackend 模擬 codex：靜態 catalog，只有 ModelPicker（model/list），沒有 ConfigSetter。
type pickerOnlyBackend struct {
	swapBackend
	options  []modelOption
	listCall int
}

func (b *pickerOnlyBackend) ModelOptions() ([]modelOption, error) {
	b.listCall++
	return b.options, nil
}

func newPickerOnlyBackend() *pickerOnlyBackend {
	return &pickerOnlyBackend{
		swapBackend: swapBackend{name: "codex", log: &[]string{}, caps: Capabilities{ModelList: true}},
		options: []modelOption{
			{ID: "gpt-5", Name: "GPT 5"},
			{ID: "gpt-6", Name: "GPT 6"},
		},
	}
}

func typeInput(frontend *ui, value string) {
	frontend.input, frontend.cursor = []rune(value), len([]rune(value))
	frontend.markInputEdited()
}

// T-CFG-28：codex 類 backend（只有 ModelPicker）：/model <id> 仍是 per-turn override。
func TestModelCommandKeepsOverrideWithoutSetter(t *testing.T) {
	backend := newPickerOnlyBackend()
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend, thread: threadSummary{ID: "s1", Model: "gpt-5"}}
	if err := frontend.handleCommand("/model gpt-6"); err != nil {
		t.Fatal(err)
	}
	if frontend.overrides.model != "gpt-6" || frontend.effectiveModel() != "gpt-6" {
		t.Fatalf("override = %q", frontend.overrides.model)
	}
}

func TestModelCommandListsPickerModelsWithCurrentMarked(t *testing.T) {
	backend := newPickerOnlyBackend()
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend, thread: threadSummary{ID: "s1", Model: "gpt-5"}}
	for _, command := range []string{"/model", "/model list"} {
		if err := frontend.handleCommand(command); err != nil {
			t.Fatalf("%s: %v", command, err)
		}
	}
	plain := stripANSI(frontend.screenFrame(120, 30))
	for _, want := range []string{"* gpt-5 · GPT 5", "  gpt-6 · GPT 6"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("model list missing %q: %q", want, plain)
		}
	}
}

// T-CFG-11：codex 的 /model 參數補全（ModelPicker）與快取不變。
func TestModelArgPopupOnPickerBackendCompletesWithTab(t *testing.T) {
	backend := newPickerOnlyBackend()
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend, thread: threadSummary{ID: "s1", Model: "gpt-5"}}
	typeInput(frontend, "/model g")
	if got := strings.Join(catalogNames(frontend.commandPopupItems()), ","); got != "gpt-5,gpt-6" {
		t.Fatalf("items = %s", got)
	}
	typeInput(frontend, "/model gpt-6")
	if err := frontend.handleKey(keyEvent{typ: keyTab}); err != nil {
		t.Fatal(err)
	}
	if string(frontend.input) != "/model gpt-6" {
		t.Fatalf("input after Tab = %q", string(frontend.input))
	}
	// 完整 ID：Enter 直接執行（codex 走 override）。
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatal(err)
	}
	if frontend.overrides.model != "gpt-6" {
		t.Fatalf("override after Enter = %q", frontend.overrides.model)
	}
}

func TestModelArgPopupCachesPickerOptionsAndOnlyAppliesToModel(t *testing.T) {
	backend := newPickerOnlyBackend()
	frontend := &ui{screenMode: true, cwd: "/tmp", backend: backend, thread: threadSummary{ID: "s1", Model: "gpt-5"}}
	for _, value := range []string{"/model ", "/model g", "/model gp"} {
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

// 沒有 model 資訊的 dynamic backend：沒有 /model，也沒有參數 popup。
func TestModelCommandAbsentWithoutModelOption(t *testing.T) {
	frontend := &ui{screenMode: true, backend: acpLikeBackend("claude", Capabilities{})}
	if contains(catalogNames(frontend.filterSlashCommands("")), "model") {
		t.Fatal("/model present without a model option")
	}
	typeInput(frontend, "/model ")
	if frontend.commandPopupVisible() {
		t.Fatal("popup visible without a model option")
	}
}
