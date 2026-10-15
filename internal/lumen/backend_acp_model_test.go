package lumen

import (
	"strings"
	"testing"
)

func newModelSession(t *testing.T, mode string) (*fakeACPRun, SessionInfo) {
	t.Helper()
	run := startFakeACP(t, map[string]string{"FAKEACP_MODELS": mode}, nil)
	info, err := run.backend.NewSession(t.TempDir(), runtimeOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	return run, info
}

func valueIDs(option ConfigOption) string {
	ids := make([]string, 0, len(option.Values))
	for _, value := range option.Values {
		ids = append(ids, value.ID)
	}
	return strings.Join(ids, ",")
}

// model 現在只是 id=model 的一般 select option：flat 與 grouped 都展平。
func TestACPModelOptionIsGenericSelect(t *testing.T) {
	for _, mode := range []string{"config", "grouped"} {
		run, info := newModelSession(t, mode)
		if info.Model != "opus" {
			t.Fatalf("%s: SessionInfo.Model = %q", mode, info.Model)
		}
		model, ok := findConfigOption(run.backend.ConfigOptions(), "model")
		if !ok || model.Category != "model" || valueIDs(model) != "default,opus,sonnet" {
			t.Fatalf("%s: model option = %#v", mode, model)
		}
		if model.Values[1].Name != "Opus 5.5" || model.Values[1].Description != "complex work" || !model.Values[1].Current || model.Values[0].Current {
			t.Fatalf("%s: values = %#v", mode, model.Values)
		}
		if !run.backend.Caps().ModelList {
			t.Fatalf("%s: caps = %#v", mode, run.backend.Caps())
		}
	}
}

func TestACPModelSwitchUsesSetConfigOption(t *testing.T) {
	run, _ := newModelSession(t, "config")
	got, err := run.backend.SetConfigOption("sess-new", "model", "sonnet")
	if err != nil || got != "sonnet" {
		t.Fatalf("SetConfigOption() = %q, %v", got, err)
	}
	requests := setConfigRequests(t, run)
	if len(requests) != 1 || requests[0]["sessionId"] != "sess-new" || requests[0]["configId"] != "model" || requests[0]["value"] != "sonnet" {
		t.Fatalf("requests = %v", requests)
	}
	model, _ := findConfigOption(run.backend.ConfigOptions(), "model")
	if model.Current != "sonnet" || !model.Values[2].Current || model.Values[1].Current {
		t.Fatalf("model after switch = %#v", model)
	}
}

// agent 推送 config_option_update 改 model -> 清單與 current 更新。
func TestACPConfigOptionUpdateChangesModel(t *testing.T) {
	run, _ := newModelSession(t, "config")
	events := run.runTurn(t, "sess-new", "modelpush")
	var current string
	for _, event := range configEventsOf(events) {
		model, _ := findConfigOption(event.Options, "model")
		current = model.Current
	}
	if current != "sonnet" {
		t.Fatalf("EvConfigOptions model = %q", current)
	}
	if model, _ := findConfigOption(run.backend.ConfigOptions(), "model"); model.Current != "sonnet" {
		t.Fatalf("backend model = %#v", model)
	}
}

func TestACPResumeReportsModel(t *testing.T) {
	run := startFakeACP(t, map[string]string{"FAKEACP_MODELS": "config", "FAKEACP_CAPS": "resume"}, nil)
	info, err := run.backend.ResumeSession("old", t.TempDir(), runtimeOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "opus" || !run.backend.Caps().ModelList {
		t.Fatalf("model = %q caps = %#v", info.Model, run.backend.Caps())
	}
}
