package lumen

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// configBackend 是 ACP-like backend：dynamic catalog，實作 ConfigSetter 並記錄呼叫。
type configBackend struct {
	swapBackend
	calls  [][3]string
	setErr error
}

func (b *configBackend) SetConfigOption(sessionID, id, value string) (string, error) {
	b.calls = append(b.calls, [3]string{sessionID, id, value})
	if b.setErr != nil {
		return "", b.setErr
	}
	return value, nil
}

func selectOption(id, name, category, current string, values ...string) ConfigOption {
	option := ConfigOption{ID: id, Name: name, Category: category}
	for _, value := range values {
		option.Values = append(option.Values, modelOption{ID: value, Name: strings.ToUpper(value[:1]) + value[1:]})
	}
	return option.withCurrent(current)
}

func sampleConfigOptions() []ConfigOption {
	model := selectOption("model", "Model", "model", "opus", "default", "opus", "sonnet")
	model.Description = "AI model to use"
	model.Values[1].Description = "complex work"
	return []ConfigOption{
		selectOption("mode", "Mode", "mode", "default", "default", "acceptEdits", "plan"),
		model,
		selectOption("effort", "Effort", "thought_level", "high", "low", "high", "xhigh"),
		selectOption("style", "Style", "", "quiet", "quiet", "loud"),
	}
}

func newConfigUI() (*ui, *configBackend) {
	backend := &configBackend{swapBackend: *acpLikeBackend("claude", Capabilities{ModelList: true})}
	frontend := &ui{screenMode: true, cwd: "/tmp", branch: "main", backend: backend, thread: threadSummary{ID: "s1"}}
	frontend.handleEvent(EvConfigOptions{Options: sampleConfigOptions()})
	return frontend, backend
}

// T-CFG-02
func TestCatalogHasOneCommandPerOptionWithValues(t *testing.T) {
	frontend, _ := newConfigUI()
	names := catalogNames(frontend.filterSlashCommands(""))
	for _, want := range []string{"mode", "model", "effort", "style"} {
		if !contains(names, want) {
			t.Fatalf("/%s missing from catalog: %v", want, names)
		}
	}
	command, _ := frontend.slashCommandByName("model")
	if command.description != "Model · AI model to use" || command.configID != "model" {
		t.Fatalf("model command = %#v", command)
	}
	// 沒有 value 的 option 不產生 command。
	frontend.handleEvent(EvConfigOptions{Options: append(sampleConfigOptions(), ConfigOption{ID: "empty", Name: "Empty", Current: "x"})})
	if contains(catalogNames(frontend.filterSlashCommands("")), "empty") {
		t.Fatal("option without values produced a command")
	}
}

// T-CFG-14
func TestModelCommandComesFromModelOptionOnly(t *testing.T) {
	frontend, _ := newConfigUI()
	if !contains(catalogNames(frontend.filterSlashCommands("")), "model") {
		t.Fatal("/model missing despite a model option")
	}
	frontend.handleEvent(EvConfigOptions{Options: sampleConfigOptions()[2:]})
	if contains(catalogNames(frontend.filterSlashCommands("")), "model") {
		t.Fatal("/model present without a model option")
	}
}

// T-CFG-12, T-CFG-13
func TestCatalogPrecedenceLocalThenOptionThenAgent(t *testing.T) {
	frontend, _ := newConfigUI()
	frontend.handleEvent(EvConfigOptions{Options: append(sampleConfigOptions(),
		selectOption("status", "Status", "", "a", "a", "b"),
		selectOption("Fast", "Fast mode", "model_config", "off", "on", "off"))})
	frontend.handleEvent(EvCommandsUpdated{Commands: []SlashCommand{
		{Name: "fast", Description: "agent fast"}, {Name: "compact", Description: "agent compact"}, {Name: "style", Description: "agent style"},
	}})
	counts := map[string]int{}
	for _, name := range catalogNames(frontend.filterSlashCommands("")) {
		counts[name]++
	}
	for _, name := range []string{"status", "fast", "style", "compact"} {
		if counts[name] != 1 {
			t.Fatalf("/%s appears %d times", name, counts[name])
		}
	}
	if command, _ := frontend.slashCommandByName("status"); command.configID != "" || command.agent {
		t.Fatalf("/status is not the local command: %#v", command)
	}
	if command, _ := frontend.slashCommandByName("fast"); command.configID != "Fast" || command.agent {
		t.Fatalf("/fast should be the option: %#v", command)
	}
	if command, _ := frontend.slashCommandByName("compact"); !command.agent {
		t.Fatalf("/compact should stay the agent command: %#v", command)
	}
}

// T-CFG-03
func TestUnknownCategoryOptionListsValuesWithCurrentMarked(t *testing.T) {
	frontend, _ := newConfigUI()
	for _, command := range []string{"/style", "/style list"} {
		if err := frontend.handleCommand(command); err != nil {
			t.Fatalf("%s: %v", command, err)
		}
	}
	plain := stripANSI(frontend.screenFrame(120, 30))
	for _, want := range []string{"style values", "* quiet · Quiet", "  loud · Loud"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("list missing %q: %q", want, plain)
		}
	}
	if err := frontend.handleCommand("/model"); err != nil {
		t.Fatal(err)
	}
	plain = stripANSI(frontend.screenFrame(120, 30))
	for _, want := range []string{"models", "* opus · Opus · complex work", "  sonnet · Sonnet"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("model list missing %q: %q", want, plain)
		}
	}
}

// T-CFG-04
func TestOptionCommandSwitchesValue(t *testing.T) {
	frontend, backend := newConfigUI()
	if err := frontend.handleCommand("/style loud"); err != nil {
		t.Fatal(err)
	}
	if len(backend.calls) != 1 || backend.calls[0] != [3]string{"s1", "style", "loud"} {
		t.Fatalf("SetConfigOption calls = %v", backend.calls)
	}
	if option, _ := findConfigOptionByID(frontend.configOptions, "style"); option.Current != "loud" || !option.Values[1].Current {
		t.Fatalf("style after switch = %#v", option)
	}
	if err := frontend.handleCommand("/model sonnet"); err != nil {
		t.Fatal(err)
	}
	if frontend.effectiveModel() != "sonnet" || frontend.overrides.model != "" {
		t.Fatalf("model = %q override = %q", frontend.effectiveModel(), frontend.overrides.model)
	}
	if plain := stripANSI(frontend.screenFrame(120, 30)); !strings.Contains(plain, "model · sonnet") {
		t.Fatalf("no confirmation: %q", plain)
	}
}

// T-CFG-05
func TestOptionCommandErrorKeepsCurrent(t *testing.T) {
	frontend, backend := newConfigUI()
	backend.setErr = errors.New(`unknown style value "x"; available: quiet, loud`)
	err := frontend.handleCommand("/style x")
	if err == nil || !strings.Contains(err.Error(), "available: quiet, loud") {
		t.Fatalf("error = %v", err)
	}
	if option, _ := findConfigOptionByID(frontend.configOptions, "style"); option.Current != "quiet" {
		t.Fatalf("current changed to %q", option.Current)
	}
}

// T-CFG-10
func TestOptionArgPopupCompletesWithTab(t *testing.T) {
	frontend, backend := newConfigUI()
	typeInput(frontend, "/style ")
	if !frontend.commandPopupVisible() {
		t.Fatal("popup hidden after `/style `")
	}
	if got := strings.Join(catalogNames(frontend.commandPopupItems()), ","); got != "quiet,loud" {
		t.Fatalf("items = %s", got)
	}
	plain := stripANSI(frontend.screenFrame(120, 30))
	for _, want := range []string{"style values", "loud", "(current)", "Tab complete"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("popup missing %q: %q", want, plain)
		}
	}
	typeInput(frontend, "/style lo")
	if got := strings.Join(catalogNames(frontend.commandPopupItems()), ","); got != "loud" {
		t.Fatalf("filtered items = %s", got)
	}
	if err := frontend.handleKey(keyEvent{typ: keyTab}); err != nil {
		t.Fatal(err)
	}
	if string(frontend.input) != "/style loud" || len(backend.calls) != 0 {
		t.Fatalf("input after Tab = %q, calls = %v", string(frontend.input), backend.calls)
	}
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatal(err)
	}
	if len(backend.calls) != 1 || backend.calls[0][2] != "loud" {
		t.Fatalf("Enter on full value: calls = %v", backend.calls)
	}

	// model 沿用過去的 popup 標題。
	typeInput(frontend, "/model ")
	if plain := stripANSI(frontend.screenFrame(120, 30)); !strings.Contains(plain, "models") || !strings.Contains(plain, "Sonnet") {
		t.Fatalf("model popup: %q", plain)
	}
}

func TestOptionArgPopupEnterWithPartialRunsExceptMode(t *testing.T) {
	frontend, backend := newConfigUI()
	typeInput(frontend, "/style lo")
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatal(err)
	}
	if len(backend.calls) != 1 || backend.calls[0][2] != "loud" {
		t.Fatalf("partial Enter on style: calls = %v", backend.calls)
	}
	typeInput(frontend, "/style ")
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatal(err)
	}
	if len(backend.calls) != 1 {
		t.Fatalf("bare /style switched: %v", backend.calls)
	}
}

// T-CFG-25
func TestModeArgPartialEnterCompletesWithoutSwitching(t *testing.T) {
	frontend, backend := newConfigUI()
	typeInput(frontend, "/mode ac")
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatal(err)
	}
	if string(frontend.input) != "/mode acceptEdits" || len(backend.calls) != 0 {
		t.Fatalf("input = %q, calls = %v", string(frontend.input), backend.calls)
	}
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatal(err)
	}
	if len(backend.calls) != 1 || backend.calls[0] != [3]string{"s1", "mode", "acceptEdits"} {
		t.Fatalf("second Enter: calls = %v", backend.calls)
	}
}

// T-CFG-15, T-CFG-16
func TestHeaderShowsModelEffortModeInOrderAndOmitsUnknown(t *testing.T) {
	frontend, _ := newConfigUI()
	if header := stripANSI(frontend.screenHeaderLine(120)); !strings.Contains(header, "opus · high · default") {
		t.Fatalf("header = %q", header)
	}
	if label := frontend.screenStatusLabel(); !strings.Contains(label, "opus (high) · default") {
		t.Fatalf("dock = %q", label)
	}
	frontend.handleEvent(EvConfigOptions{Options: []ConfigOption{sampleConfigOptions()[1]}})
	header := stripANSI(frontend.screenHeaderLine(120))
	label := frontend.screenStatusLabel()
	if !strings.Contains(header, "opus") || strings.Contains(header, "high") || strings.Contains(header, "default") || strings.Contains(label, "(") {
		t.Fatalf("unknown categories shown: header %q dock %q", header, label)
	}
	frontend.handleEvent(EvConfigOptions{Options: sampleConfigOptions()[3:]})
	if header := stripANSI(frontend.screenHeaderLine(120)); strings.Contains(header, "opus") || strings.Contains(header, "configured") {
		t.Fatalf("header = %q", header)
	}
}

// T-CFG-17
func TestDockDoesNotRepeatModeWhenPermissionWarningShown(t *testing.T) {
	frontend, _ := newConfigUI()
	frontend.handleEvent(EvConfigOptions{Options: func() []ConfigOption {
		options := sampleConfigOptions()
		options[0] = options[0].withCurrent("plan")
		return options
	}()})
	frontend.handleEvent(EvModeWarning{Mode: "plan", Text: "permissions: plan (from agent settings)"})
	label := frontend.screenStatusLabel()
	if strings.Count(label, "plan") != 1 || !strings.Contains(label, "permissions: plan") {
		t.Fatalf("dock = %q", label)
	}
}

// T-CFG-18
func TestCorallinePayloadCarriesModeWhenKnown(t *testing.T) {
	frontend, _ := newConfigUI()
	encoded, _ := json.Marshal(frontend.corallinePayload())
	for _, want := range []string{`"display_name":"opus"`, `"level":"high"`, `"mode":{"id":"default"}`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("payload missing %s: %s", want, encoded)
		}
	}
	frontend.handleEvent(EvConfigOptions{Options: sampleConfigOptions()[1:2]})
	encoded, _ = json.Marshal(frontend.corallinePayload())
	if strings.Contains(string(encoded), `"mode"`) || strings.Contains(string(encoded), `"effort"`) {
		t.Fatalf("unknown categories leaked: %s", encoded)
	}
}

// T-CFG-19
func TestStatusListsEveryConfigOption(t *testing.T) {
	frontend, _ := newConfigUI()
	if err := frontend.handleCommand("/status"); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(frontend.screenFrame(120, 40))
	for _, want := range []string{"options", "mode", "default · Mode", "model", "opus · Model", "effort", "high · Effort", "style", "quiet · Style"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("/status missing %q: %q", want, plain)
		}
	}
	bare := &ui{screenMode: true, backend: &fakeBackend{}}
	_ = bare.handleCommand("/status")
	if strings.Contains(stripANSI(bare.screenFrame(120, 30)), "options") {
		t.Fatal("/status printed an options section without config options")
	}
}

// T-CFG-22（取代 T-UX-07 的 EvEffortChanged）
func TestConfigOptionsEventReplacesHeaderValues(t *testing.T) {
	frontend := &ui{screenMode: true, branch: "main", thread: threadSummary{Model: "opus", ReasoningEffort: "high"}}
	if header := stripANSI(frontend.screenHeaderLine(100)); !strings.Contains(header, "opus · high") {
		t.Fatalf("pre-config header = %q", header)
	}
	frontend.handleEvent(EvConfigOptions{Options: []ConfigOption{
		selectOption("model", "Model", "model", "sonnet", "sonnet"),
		selectOption("effort", "Effort", "thought_level", "xhigh", "xhigh"),
	}})
	if header := stripANSI(frontend.screenHeaderLine(100)); !strings.Contains(header, "sonnet · xhigh") {
		t.Fatalf("header = %q", header)
	}
	// effort option 消失：不退回 thread 的舊值。
	frontend.handleEvent(EvConfigOptions{Options: []ConfigOption{selectOption("model", "Model", "model", "sonnet", "sonnet")}})
	if header := stripANSI(frontend.screenHeaderLine(100)); strings.Contains(header, "xhigh") || strings.Contains(header, "high") {
		t.Fatalf("stale effort in header = %q", header)
	}
}

// T-CFG-24
func TestBackendSwitchClearsConfigOptions(t *testing.T) {
	h := newSwitchHarness(t)
	h.ui.configOptions = sampleConfigOptions()
	h.ui.adoptSwitchedBackend(SessionInfo{ID: "claude-session"})
	if h.ui.configOptions != nil {
		t.Fatalf("config options survived backend switch: %v", h.ui.configOptions)
	}
}
