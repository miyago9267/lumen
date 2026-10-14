package lumen

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// useConfigDir 把 XDG_CONFIG_HOME 指到暫存目錄；content 非空時寫入 backends.json（mode 0600）。
func useConfigDir(t *testing.T, content string) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	path := filepath.Join(base, "lumen", "backends.json")
	if content != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// T-P3-01：無設定檔時內建三個 backend 可用；claude 不含 npx。
func TestBuiltinBackendRegistry(t *testing.T) {
	useConfigDir(t, "")
	registry, err := loadBackendRegistry()
	if err != nil {
		t.Fatalf("loadBackendRegistry() error = %v", err)
	}
	if got := registry.Names(); !reflect.DeepEqual(got, []string{"claude", "gemini", "codex"}) {
		t.Fatalf("Names() = %v", got)
	}
	claude, _ := registry.Spec("claude")
	if claude.Type != "acp" || claude.Command != "claude-agent-acp" || len(claude.Args) != 0 {
		t.Fatalf("claude spec = %#v", claude)
	}
	if strings.Contains(claude.Command, "npx") {
		t.Fatalf("claude command uses npx: %#v", claude)
	}
	gemini, _ := registry.Spec("gemini")
	if gemini.Type != "acp" || gemini.Command != "gemini" || !reflect.DeepEqual(gemini.Args, []string{"--acp"}) {
		t.Fatalf("gemini spec = %#v", gemini)
	}
	if codex, _ := registry.Spec("codex"); codex.Type != "codex" {
		t.Fatalf("codex spec = %#v", codex)
	}
	cfg := claude.acpConfig()
	if cfg.InstallHint != claudeACPInstallHint || cfg.SessionMeta == nil {
		t.Fatalf("claude acpConfig = %#v", cfg)
	}
	if gcfg := gemini.acpConfig(); gcfg.SessionMeta != nil || gcfg.InstallHint != "" {
		t.Fatalf("gemini acpConfig carries claude settings: %#v", gcfg)
	}
}

// T-P3-02：同名 entry 覆蓋內建、可新增 entry；type / command 驗證錯誤。
func TestBackendConfigOverrideAndValidation(t *testing.T) {
	useConfigDir(t, `{"default":"mine","backends":{
		"claude":{"type":"acp","command":"npx","args":["-y","@agentclientprotocol/claude-agent-acp@0.90.0"]},
		"mine":{"type":"acp","command":"my-agent","args":["--stdio"]}}}`)
	registry, err := loadBackendRegistry()
	if err != nil {
		t.Fatalf("loadBackendRegistry() error = %v", err)
	}
	if got := registry.Names(); !reflect.DeepEqual(got, []string{"claude", "gemini", "codex", "mine"}) {
		t.Fatalf("Names() = %v", got)
	}
	claude, _ := registry.Spec("claude")
	if claude.Command != "npx" || len(claude.Args) != 2 {
		t.Fatalf("claude override = %#v", claude)
	}
	if cfg := claude.acpConfig(); cfg.SessionMeta == nil || cfg.InstallHint != "" {
		t.Fatalf("opted-in npx claude config = %#v (want claude session meta, no install hint)", cfg)
	}
	if spec, err := registry.Resolve("", ""); err != nil || spec.Name != "mine" {
		t.Fatalf("Resolve() = %#v, %v", spec, err)
	}

	for name, content := range map[string]string{
		"bad type":        `{"backends":{"x":{"type":"http","command":"a"}}}`,
		"missing type":    `{"backends":{"x":{"command":"a"}}}`,
		"acp no command":  `{"backends":{"x":{"type":"acp"}}}`,
		"blank command":   `{"backends":{"x":{"type":"acp","command":"  "}}}`,
		"codex w/command": `{"backends":{"x":{"type":"codex","command":"a"}}}`,
		"bad name":        `{"backends":{"bad name":{"type":"codex"}}}`,
		"default unknown": `{"default":"nope"}`,
		"invalid json":    `{`,
		"args not array":  `{"backends":{"x":{"type":"acp","command":"a","args":"--acp"}}}`,
		"trailing data":   `{} {}`,
	} {
		useConfigDir(t, content)
		if _, err := loadBackendRegistry(); err == nil {
			t.Errorf("%s: loadBackendRegistry() succeeded", name)
		}
	}
}

// T-P3-03 的 registry 部分：優先序 flag > env > config default > claude；未知 name 列出可用 backend。
func TestBackendResolvePrecedence(t *testing.T) {
	useConfigDir(t, `{"default":"gemini"}`)
	registry, err := loadBackendRegistry()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ flag, env, want string }{
		{"codex", "gemini", "codex"},
		{"", "codex", "codex"},
		{"", "", "gemini"},
	}
	for _, tc := range cases {
		spec, err := registry.Resolve(tc.flag, tc.env)
		if err != nil || spec.Name != tc.want {
			t.Errorf("Resolve(%q, %q) = %q, %v; want %q", tc.flag, tc.env, spec.Name, err, tc.want)
		}
	}
	useConfigDir(t, "")
	registry, _ = loadBackendRegistry()
	if spec, err := registry.Resolve("", ""); err != nil || spec.Name != "claude" {
		t.Errorf("Resolve() default = %q, %v", spec.Name, err)
	}
	_, err = registry.Resolve("nope", "")
	if err == nil || !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), "claude, gemini, codex") {
		t.Errorf("unknown backend error = %v", err)
	}
}

// T-P3-12：只讀 XDG config 路徑，不讀 cwd / 專案目錄；相對的 XDG_CONFIG_HOME 無效。
func TestBackendConfigIgnoresProjectDirectory(t *testing.T) {
	useConfigDir(t, "")
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "backends.json"), []byte(`{"default":"evil","backends":{"evil":{"type":"acp","command":"x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"lumen/backends.json", ".lumen/backends.json", ".config/lumen/backends.json"} {
		path := filepath.Join(project, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"default":"evil"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(project)
	registry, err := loadBackendRegistry()
	if err != nil {
		t.Fatalf("loadBackendRegistry() error = %v", err)
	}
	if _, ok := registry.Spec("evil"); ok {
		t.Fatal("project backends.json was read")
	}
	if spec, _ := registry.Resolve("", ""); spec.Name != "claude" {
		t.Fatalf("default = %q", spec.Name)
	}

	// 相對的 XDG_CONFIG_HOME 不得把設定檔導向 cwd。
	t.Setenv("XDG_CONFIG_HOME", ".")
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := backendConfigPath()
	if err != nil || path != filepath.Join(home, ".config", "lumen", "backends.json") {
		t.Fatalf("backendConfigPath() = %q, %v", path, err)
	}
}

// T-P3-13：owner 不是目前 uid、或 group / world 可寫 -> 錯誤並點名檔案。
func TestBackendConfigRejectsUntrustedFile(t *testing.T) {
	path := useConfigDir(t, `{"default":"claude"}`)
	for _, mode := range []os.FileMode{0o620, 0o602, 0o666, 0o660} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		_, err := loadBackendRegistry()
		if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "writable") {
			t.Errorf("mode %04o: error = %v", mode, err)
		}
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBackendRegistry(); err != nil {
		t.Errorf("mode 0644 should be accepted: %v", err)
	}

	real := backendConfigUID
	t.Cleanup(func() { backendConfigUID = real })
	backendConfigUID = func() int { return real() + 1 }
	_, err := loadBackendRegistry()
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "not owned") {
		t.Errorf("foreign owner error = %v", err)
	}
}

// T-P3-14：env 與白名單以外的 key -> 錯誤並點名該 key。
func TestBackendConfigRejectsUnknownKeys(t *testing.T) {
	cases := map[string]string{
		`backends.claude.env`:   `{"backends":{"claude":{"type":"acp","command":"a","env":{"K":"V"}}}}`,
		`backends.x.cwd`:        `{"backends":{"x":{"type":"acp","command":"a","cwd":"/"}}}`,
		`"env"`:                 `{"env":{"K":"V"}}`,
		`"model"`:               `{"model":"x"}`,
		`backends.x.headers`:    `{"backends":{"x":{"type":"acp","command":"a","headers":{}}}}`,
		`backends.codex.Args`:   `{"backends":{"codex":{"type":"codex","Args":[]}}}`,
		`backends.claude.Env`:   `{"backends":{"claude":{"type":"acp","command":"a","Env":{}}}}`,
		`backends.x.mcpServers`: `{"backends":{"x":{"type":"acp","command":"a","mcpServers":[]}}}`,
	}
	for key, content := range cases {
		useConfigDir(t, content)
		_, err := loadBackendRegistry()
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s: error = %v", key, err)
		}
	}
}

// T-P3-03：--backend 旗標與 selectBackend 的優先序、未知 name。
func TestParseBackendFlagAndSelection(t *testing.T) {
	useConfigDir(t, `{"default":"gemini"}`)
	opts, err := parseOptions([]string{"--backend", "codex", "hello"})
	if err != nil || opts.backend != "codex" || opts.prompt != "hello" {
		t.Fatalf("parseOptions() = %#v, %v", opts, err)
	}
	cases := []struct {
		args      []string
		env, want string
	}{
		{[]string{"--backend", "claude"}, "codex", "claude"},
		{nil, "codex", "codex"},
		{nil, "", "gemini"},
	}
	for _, tc := range cases {
		opts, err := parseOptions(tc.args)
		if err != nil {
			t.Fatal(err)
		}
		_, spec, err := selectBackend(opts, tc.env)
		if err != nil || spec.Name != tc.want {
			t.Errorf("selectBackend(%v, %q) = %q, %v; want %q", tc.args, tc.env, spec.Name, err, tc.want)
		}
	}
	opts, _ = parseOptions([]string{"--backend", "nope"})
	_, _, err = selectBackend(opts, "")
	if err == nil || !strings.Contains(err.Error(), "claude, gemini, codex") {
		t.Errorf("unknown backend error = %v", err)
	}
}

// T-P3-04：非 codex backend 帶 codex 專屬旗標 -> 明確錯誤，指名旗標與 backend。
func TestCodexOnlyFlagsRejectedOnOtherBackends(t *testing.T) {
	useConfigDir(t, "")
	for _, tc := range []struct {
		args []string
		flag string
	}{
		{[]string{"--model", "x"}, "--model"},
		{[]string{"--reasoning-effort", "high"}, "--reasoning-effort"},
		{[]string{"--sandbox", "read-only"}, "--sandbox"},
		{[]string{"--approval-policy", "never"}, "--approval-policy"},
		{[]string{"--ask-for-approval", "never"}, "--ask-for-approval"},
	} {
		for _, backend := range []string{"claude", "gemini"} {
			opts, err := parseOptions(append([]string{"--backend", backend}, tc.args...))
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = selectBackend(opts, "")
			if err == nil || !strings.Contains(err.Error(), tc.flag) || !strings.Contains(err.Error(), backend) || !strings.Contains(err.Error(), "only supported by backend codex") {
				t.Errorf("%v on %s: error = %v", tc.args, backend, err)
			}
		}
		opts, _ := parseOptions(append([]string{"--backend", "codex"}, tc.args...))
		if _, _, err := selectBackend(opts, ""); err != nil {
			t.Errorf("%v on codex: error = %v", tc.args, err)
		}
	}
	// 預設 backend（claude）也要擋。
	opts, _ := parseOptions([]string{"--sandbox", "read-only"})
	if _, _, err := selectBackend(opts, ""); err == nil {
		t.Error("--sandbox on default backend was silently accepted")
	}
}

// T-P3-03 / T-P3-13 / T-P3-14：設定或選擇錯誤時 Run 以 exit code 2 結束，不啟動任何 backend。
func TestRunConfigErrorsExitTwo(t *testing.T) {
	t.Setenv("LUMEN_CODEX_BIN", "/nonexistent/codex")
	t.Setenv("LUMEN_BACKEND", "")
	useConfigDir(t, "")
	if code := Run([]string{"--backend", "nope", "hi"}); code != 2 {
		t.Errorf("unknown backend exit = %d", code)
	}
	if code := Run([]string{"--sandbox", "read-only", "hi"}); code != 2 {
		t.Errorf("codex flag on claude exit = %d", code)
	}
	t.Setenv("LUMEN_BACKEND", "nope")
	if code := Run([]string{"hi"}); code != 2 {
		t.Errorf("LUMEN_BACKEND unknown exit = %d", code)
	}
	t.Setenv("LUMEN_BACKEND", "")
	path := useConfigDir(t, `{"backends":{"x":{"type":"acp","command":"a","env":{"K":"V"}}}}`)
	if code := Run([]string{"hi"}); code != 2 {
		t.Errorf("env key exit = %d", code)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"hi"}); code != 2 {
		t.Errorf("writable config exit = %d", code)
	}
}

// T-P3-05：lumen codex ... 仍直接 passthrough 給官方 codex，不受 backend 預設與設定檔影響。
func TestNativeCodexPassthroughIgnoresBackendSelection(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LUMEN_CODEX_BIN", script)
	t.Setenv("LUMEN_BACKEND", "gemini")
	useConfigDir(t, `{"invalid json`)
	if code := Run([]string{"codex", "--version"}); code != 7 {
		t.Errorf("lumen codex exit = %d, want 7", code)
	}
	if code := Run([]string{"exec", "hi"}); code != 7 {
		t.Errorf("lumen exec exit = %d, want 7", code)
	}
	if args, ok := nativeCodexInvocation([]string{"--backend", "claude", "hello"}); ok {
		t.Errorf("--backend prompt treated as native codex: %v", args)
	}
}

// openThread：--resume / --continue 對沒有該 capability 的 backend 明確報錯，不默默開新 session。
func TestOpenThreadChecksCapabilities(t *testing.T) {
	backend := &fakeBackend{}
	if _, err := openThread(backend, "/tmp", options{resume: "abc"}); err == nil || !strings.Contains(err.Error(), "fake cannot resume sessions") {
		t.Errorf("resume error = %v", err)
	}
	if _, err := openThread(backend, "/tmp", options{continue_: true}); err == nil || !strings.Contains(err.Error(), "backend fake cannot list sessions") {
		t.Errorf("continue error = %v", err)
	}
	if info, err := openThread(backend, "/tmp", options{}); err != nil || info.ID != "fake-session" {
		t.Errorf("new session = %#v, %v", info, err)
	}
}
