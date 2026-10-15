package lumen

import (
	"encoding/json"
	"strings"
	"testing"
)

func modelIDs(options []modelOption) []string {
	ids := make([]string, 0, len(options))
	for _, option := range options {
		ids = append(ids, option.ID)
	}
	return ids
}

func newModelSession(t *testing.T, mode string) (*fakeACPRun, SessionInfo) {
	t.Helper()
	run := startFakeACP(t, map[string]string{"FAKEACP_MODELS": mode}, nil)
	info, err := run.backend.NewSession(t.TempDir(), runtimeOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	return run, info
}

// session/new 帶 category=model 的 configOption -> caps、current model、清單。
func TestACPModelOptionsFromConfigOptions(t *testing.T) {
	run, info := newModelSession(t, "config")
	if info.Model != "opus" {
		t.Fatalf("SessionInfo.Model = %q, want opus", info.Model)
	}
	if caps := run.backend.Caps(); !caps.ModelList || !caps.ModelSwitch {
		t.Fatalf("caps = %#v", caps)
	}
	options, err := run.backend.ModelOptions()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(modelIDs(options), ","); got != "default,opus,sonnet" {
		t.Fatalf("model ids = %s", got)
	}
	if options[1].Name != "Opus 5.5" || options[1].Description != "complex work" || !options[1].Current || options[0].Current {
		t.Fatalf("options = %#v", options)
	}
}

func TestACPModelOptionsGroupedAreFlattened(t *testing.T) {
	run, _ := newModelSession(t, "grouped")
	options, err := run.backend.ModelOptions()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(modelIDs(options), ","); got != "default,opus,sonnet" {
		t.Fatalf("grouped model ids = %s", got)
	}
}

func TestACPNoModelInfoMeansNoModelCaps(t *testing.T) {
	run, info := newModelSession(t, "")
	if info.Model != "" {
		t.Fatalf("Model = %q", info.Model)
	}
	if caps := run.backend.Caps(); caps.ModelList || caps.ModelSwitch {
		t.Fatalf("caps = %#v", caps)
	}
	if _, err := run.backend.ModelOptions(); err == nil {
		t.Fatal("ModelOptions succeeded without model info")
	}
	if _, err := run.backend.SetModel("sess-new", "opus"); err == nil {
		t.Fatal("SetModel succeeded without model info")
	}
}

func TestACPSetModelUsesSetConfigOption(t *testing.T) {
	run, _ := newModelSession(t, "config")
	got, err := run.backend.SetModel("sess-new", "sonnet")
	if err != nil || got != "sonnet" {
		t.Fatalf("SetModel() = %q, %v", got, err)
	}
	requests := fakeACPRequests(t, run.logPath, "session/set_config_option")
	if len(requests) != 1 {
		t.Fatalf("set_config_option requests = %d", len(requests))
	}
	var params map[string]string
	if err := json.Unmarshal(requests[0], &params); err != nil {
		t.Fatal(err)
	}
	if params["sessionId"] != "sess-new" || params["configId"] != "model" || params["value"] != "sonnet" {
		t.Fatalf("params = %v", params)
	}
	options, _ := run.backend.ModelOptions()
	for _, option := range options {
		if option.Current != (option.ID == "sonnet") {
			t.Fatalf("current marker wrong after switch: %#v", options)
		}
	}
}

func TestACPSetModelResolvesNameCaseInsensitively(t *testing.T) {
	run, _ := newModelSession(t, "config")
	got, err := run.backend.SetModel("sess-new", "SONNET")
	if err != nil || got != "sonnet" {
		t.Fatalf("SetModel(SONNET) = %q, %v", got, err)
	}
	got, err = run.backend.SetModel("sess-new", "opus 5.5")
	if err != nil || got != "opus" {
		t.Fatalf("SetModel(by name) = %q, %v", got, err)
	}
}

func TestACPSetModelUnknownListsValidIDs(t *testing.T) {
	run, _ := newModelSession(t, "config")
	_, err := run.backend.SetModel("sess-new", "gpt-9")
	if err == nil || !strings.Contains(err.Error(), `unknown model "gpt-9"`) || !strings.Contains(err.Error(), "default, opus, sonnet") {
		t.Fatalf("error = %v", err)
	}
	if n := len(fakeACPRequests(t, run.logPath, "session/set_config_option")); n != 0 {
		t.Fatalf("unknown model still sent %d requests", n)
	}
}

func TestACPSetModelAgentErrorSurfaces(t *testing.T) {
	run := startFakeACP(t, map[string]string{"FAKEACP_MODELS": "config", "FAKEACP_SET_ERROR": "1"}, nil)
	if _, err := run.backend.NewSession(t.TempDir(), runtimeOverrides{}); err != nil {
		t.Fatal(err)
	}
	if _, err := run.backend.SetModel("sess-new", "sonnet"); err == nil || !strings.Contains(err.Error(), "invalid value") {
		t.Fatalf("error = %v", err)
	}
	options, _ := run.backend.ModelOptions()
	if !options[1].Current {
		t.Fatalf("failed switch changed current model: %#v", options)
	}
}

// 沒有 configOptions、只有 unstable models.availableModels -> session/set_model fallback。
func TestACPLegacyModelsFallback(t *testing.T) {
	run, info := newModelSession(t, "legacy")
	if info.Model != "opus" {
		t.Fatalf("Model = %q", info.Model)
	}
	if caps := run.backend.Caps(); !caps.ModelList || !caps.ModelSwitch {
		t.Fatalf("caps = %#v", caps)
	}
	options, _ := run.backend.ModelOptions()
	if got := strings.Join(modelIDs(options), ","); got != "default,opus,sonnet" {
		t.Fatalf("legacy ids = %s", got)
	}
	if got, err := run.backend.SetModel("sess-new", "sonnet"); err != nil || got != "sonnet" {
		t.Fatalf("SetModel() = %q, %v", got, err)
	}
	requests := fakeACPRequests(t, run.logPath, "session/set_model")
	if len(requests) != 1 || !strings.Contains(string(requests[0]), `"modelId":"sonnet"`) {
		t.Fatalf("set_model requests = %s", requests)
	}
	if len(fakeACPRequests(t, run.logPath, "session/set_config_option")) != 0 {
		t.Fatal("legacy fallback used set_config_option")
	}
	options, _ = run.backend.ModelOptions()
	if !options[2].Current {
		t.Fatalf("current not updated: %#v", options)
	}
}

// agent 推送 config_option_update 改 model -> 更新 current 並送 EvModelChanged。
func TestACPConfigOptionUpdateChangesModel(t *testing.T) {
	run, _ := newModelSession(t, "config")
	events := run.runTurn(t, "sess-new", "modelpush")
	var changed *EvModelChanged
	for _, event := range events {
		if event, ok := event.(EvModelChanged); ok {
			changed = &event
		}
	}
	if changed == nil || changed.Model != "sonnet" {
		t.Fatalf("EvModelChanged = %#v", changed)
	}
	options, _ := run.backend.ModelOptions()
	if !options[2].Current {
		t.Fatalf("current not updated: %#v", options)
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
