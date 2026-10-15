package lumen

import (
	"encoding/json"
	"strings"
	"testing"
)

// drainEvents 取走 channel 內目前已排隊的 events（NewSession / SetConfigOption 回傳前就已送出）。
func drainEvents(events <-chan Event) []Event {
	var all []Event
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return all
			}
			all = append(all, event)
		default:
			return all
		}
	}
}

func configEventsOf(events []Event) []EvConfigOptions {
	var out []EvConfigOptions
	for _, event := range events {
		if event, ok := event.(EvConfigOptions); ok {
			out = append(out, event)
		}
	}
	return out
}

func findConfigOption(options []ConfigOption, id string) (ConfigOption, bool) {
	for _, option := range options {
		if option.ID == id {
			return option, true
		}
	}
	return ConfigOption{}, false
}

func configIDs(options []ConfigOption) string {
	ids := make([]string, 0, len(options))
	for _, option := range options {
		ids = append(ids, option.ID)
	}
	return strings.Join(ids, ",")
}

func newConfigSession(t *testing.T, env map[string]string) (*fakeACPRun, SessionInfo) {
	t.Helper()
	merged := map[string]string{"FAKEACP_MODELS": "config", "FAKEACP_EXTRA": "1", "FAKEACP_EFFORT": "high"}
	for key, value := range env {
		merged[key] = value
	}
	run := startFakeACP(t, merged, nil)
	info, err := run.backend.NewSession(t.TempDir(), runtimeOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	return run, info
}

func setConfigRequests(t *testing.T, run *fakeACPRun) []map[string]string {
	t.Helper()
	var out []map[string]string
	for _, raw := range fakeACPRequests(t, run.logPath, "session/set_config_option") {
		var params map[string]string
		if err := json.Unmarshal(raw, &params); err != nil {
			t.Fatal(err)
		}
		out = append(out, params)
	}
	return out
}

// T-CFG-01
func TestACPSessionNewEmitsEverySelectOption(t *testing.T) {
	run, info := newConfigSession(t, nil)
	if info.Model != "opus" || info.ReasoningEffort != "high" {
		t.Fatalf("session info = %q/%q", info.Model, info.ReasoningEffort)
	}
	configs := configEventsOf(drainEvents(run.events))
	if len(configs) != 1 {
		t.Fatalf("EvConfigOptions events = %d", len(configs))
	}
	// brief 是 boolean，被忽略；style 是 Lumen 不認得的 option，照樣進清單。
	if got := configIDs(configs[0].Options); got != "mode,model,effort,style" {
		t.Fatalf("option ids = %s", got)
	}
	style, _ := findConfigOption(configs[0].Options, "style")
	if style.Current != "quiet" || style.Name != "Style" || style.Description != "Reply style" || style.Category != "" || len(style.Values) != 4 {
		t.Fatalf("style option = %#v", style)
	}
	if !style.Values[0].Current || style.Values[1].Current {
		t.Fatalf("current marker wrong: %#v", style.Values)
	}
	if caps := run.backend.Caps(); !caps.ModelList {
		t.Fatalf("caps = %#v", caps)
	}
}

// T-CFG-27：開 session 不送任何 set_config_option。
func TestACPSessionOpenNeverSetsConfig(t *testing.T) {
	run, _ := newConfigSession(t, nil)
	if n := len(setConfigRequests(t, run)); n != 0 {
		t.Fatalf("session open sent %d set_config_option requests", n)
	}
}

// T-CFG-06
func TestACPSetConfigOptionSendsRequestAndAppliesResult(t *testing.T) {
	run, _ := newConfigSession(t, nil)
	drainEvents(run.events)
	got, err := run.backend.SetConfigOption("sess-new", "style", "loud")
	if err != nil || got != "loud" {
		t.Fatalf("SetConfigOption() = %q, %v", got, err)
	}
	requests := setConfigRequests(t, run)
	if len(requests) != 1 || requests[0]["sessionId"] != "sess-new" || requests[0]["configId"] != "style" || requests[0]["value"] != "loud" {
		t.Fatalf("requests = %v", requests)
	}
	configs := configEventsOf(drainEvents(run.events))
	if len(configs) != 1 {
		t.Fatalf("EvConfigOptions events = %d", len(configs))
	}
	style, _ := findConfigOption(configs[0].Options, "style")
	if style.Current != "loud" || !style.Values[1].Current || style.Values[0].Current {
		t.Fatalf("style after set = %#v", style)
	}
}

// T-CFG-07
func TestACPSetConfigOptionUnknownValueListsChoicesWithoutRequest(t *testing.T) {
	run, _ := newConfigSession(t, nil)
	_, err := run.backend.SetConfigOption("sess-new", "style", "whisper")
	if err == nil || !strings.Contains(err.Error(), `unknown style value "whisper"`) || !strings.Contains(err.Error(), "quiet, loud, blast, shout") {
		t.Fatalf("error = %v", err)
	}
	if _, err := run.backend.SetConfigOption("sess-new", "nope", "x"); err == nil || !strings.Contains(err.Error(), "unknown option") {
		t.Fatalf("unknown option error = %v", err)
	}
	if n := len(setConfigRequests(t, run)); n != 0 {
		t.Fatalf("invalid input still sent %d requests", n)
	}
}

// T-CFG-08
func TestACPSetConfigOptionMatchesNameWhenUnambiguous(t *testing.T) {
	run, _ := newConfigSession(t, nil)
	if got, err := run.backend.SetConfigOption("sess-new", "style", "LOUD"); err != nil || got != "loud" {
		t.Fatalf("by value, case-insensitive = %q, %v", got, err)
	}
	if got, err := run.backend.SetConfigOption("sess-new", "model", "opus 5.5"); err != nil || got != "opus" {
		t.Fatalf("by name = %q, %v", got, err)
	}
	before := len(setConfigRequests(t, run))
	_, err := run.backend.SetConfigOption("sess-new", "style", "twin")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "blast, shout") {
		t.Fatalf("ambiguous error = %v", err)
	}
	if after := len(setConfigRequests(t, run)); after != before {
		t.Fatal("ambiguous query still sent a request")
	}
}

// T-CFG-09
func TestACPSetConfigOptionAgentErrorKeepsCurrent(t *testing.T) {
	run, _ := newConfigSession(t, map[string]string{"FAKEACP_SET_ERROR": "1"})
	drainEvents(run.events)
	if _, err := run.backend.SetConfigOption("sess-new", "style", "loud"); err == nil || !strings.Contains(err.Error(), "invalid value") {
		t.Fatalf("error = %v", err)
	}
	if configs := configEventsOf(drainEvents(run.events)); len(configs) != 0 {
		t.Fatalf("failed set emitted %d config events", len(configs))
	}
	options := run.backend.ConfigOptions()
	if style, _ := findConfigOption(options, "style"); style.Current != "quiet" {
		t.Fatalf("failed set changed current: %#v", style)
	}
}

// T-CFG-20 / T-CFG-22 的 backend 部分：一次 update 帶多個變動，只送一個 event。
func TestACPConfigOptionUpdateRefreshesEveryOption(t *testing.T) {
	run, info := newConfigSession(t, nil)
	drainEvents(run.events)
	events := run.runTurn(t, info.ID, "configpush")
	configs := configEventsOf(events)
	if len(configs) != 1 {
		t.Fatalf("EvConfigOptions events = %d", len(configs))
	}
	for id, want := range map[string]string{"model": "sonnet", "effort": "xhigh", "style": "loud"} {
		if option, _ := findConfigOption(configs[0].Options, id); option.Current != want {
			t.Fatalf("%s = %q, want %q", id, option.Current, want)
		}
	}
	events = run.runTurn(t, info.ID, "effortdrop")
	configs = configEventsOf(events)
	if len(configs) != 1 {
		t.Fatalf("EvConfigOptions events after drop = %d", len(configs))
	}
	if _, ok := findConfigOption(configs[0].Options, "effort"); ok {
		t.Fatalf("effort option still present: %s", configIDs(configs[0].Options))
	}
}

// T-CFG-21
func TestACPResumeEmitsConfigOptions(t *testing.T) {
	run := startFakeACP(t, map[string]string{"FAKEACP_MODELS": "config", "FAKEACP_EXTRA": "1", "FAKEACP_EFFORT": "low", "FAKEACP_CAPS": "resume"}, nil)
	info, err := run.backend.ResumeSession("old", t.TempDir(), runtimeOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "opus" || info.ReasoningEffort != "low" {
		t.Fatalf("info = %q/%q", info.Model, info.ReasoningEffort)
	}
	configs := configEventsOf(drainEvents(run.events))
	if len(configs) != 1 || configIDs(configs[0].Options) != "mode,model,effort,style" {
		t.Fatalf("config events = %#v", configs)
	}
}

// T-CFG-23
func TestACPLegacyModelsBecomeModelOption(t *testing.T) {
	run, info := newModelSession(t, "legacy")
	if info.Model != "opus" {
		t.Fatalf("Model = %q", info.Model)
	}
	configs := configEventsOf(drainEvents(run.events))
	if len(configs) != 1 {
		t.Fatalf("config events = %d", len(configs))
	}
	model, ok := findConfigOption(configs[0].Options, "model")
	if !ok || model.Category != "model" || model.Current != "opus" || len(model.Values) != 3 {
		t.Fatalf("legacy model option = %#v", model)
	}
	if got, err := run.backend.SetConfigOption("sess-new", "model", "sonnet"); err != nil || got != "sonnet" {
		t.Fatalf("SetConfigOption() = %q, %v", got, err)
	}
	requests := fakeACPRequests(t, run.logPath, "session/set_model")
	if len(requests) != 1 || !strings.Contains(string(requests[0]), `"modelId":"sonnet"`) {
		t.Fatalf("set_model requests = %s", requests)
	}
	if len(setConfigRequests(t, run)) != 0 {
		t.Fatal("legacy model used set_config_option")
	}
	configs = configEventsOf(drainEvents(run.events))
	if len(configs) != 1 {
		t.Fatalf("config events after set = %d", len(configs))
	}
	if model, _ := findConfigOption(configs[0].Options, "model"); model.Current != "sonnet" {
		t.Fatalf("legacy current not updated: %#v", model)
	}
}

func TestACPNoConfigInfoMeansNoOptions(t *testing.T) {
	run, info := newModelSession(t, "")
	if info.Model != "" || len(configEventsOf(drainEvents(run.events))) != 0 {
		t.Fatalf("unexpected config: model %q", info.Model)
	}
	if caps := run.backend.Caps(); caps.ModelList {
		t.Fatalf("caps = %#v", caps)
	}
	if _, err := run.backend.SetConfigOption("sess-new", "model", "opus"); err == nil {
		t.Fatal("SetConfigOption succeeded without options")
	}
}

// T-CFG-26
func TestACPModeSwitchEmitsAndClearsWarning(t *testing.T) {
	run, _ := newConfigSession(t, nil)
	drainEvents(run.events)
	if _, err := run.backend.SetConfigOption("sess-new", "mode", "plan"); err != nil {
		t.Fatal(err)
	}
	var warnings []EvModeWarning
	collect := func() {
		for _, event := range drainEvents(run.events) {
			if event, ok := event.(EvModeWarning); ok {
				warnings = append(warnings, event)
			}
		}
	}
	collect()
	if len(warnings) != 1 || warnings[0].Mode != "plan" || !strings.Contains(warnings[0].Text, "permissions: plan") {
		t.Fatalf("warnings after /mode plan = %#v", warnings)
	}
	if _, err := run.backend.SetConfigOption("sess-new", "mode", "Manual"); err != nil {
		t.Fatal(err)
	}
	collect()
	if len(warnings) != 2 || warnings[1].Mode != "default" || warnings[1].Text != "" {
		t.Fatalf("warnings after back to default = %#v", warnings)
	}
}
