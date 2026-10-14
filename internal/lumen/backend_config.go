package lumen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

const (
	backendTypeACP   = "acp"
	backendTypeCodex = "codex"

	defaultBackendName = "claude"
	backendConfigFile  = "backends.json"
)

var backendNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// backendConfigUID 回傳目前 uid；測試可替換以模擬「檔案屬於其他使用者」。
var backendConfigUID = os.Getuid

// backendSpec 是 registry 中的一個 backend：名稱、種類與（ACP 才有的）啟動指令。
// 它只描述「如何啟動」，不含 env；args 不得含 secret。
type backendSpec struct {
	Name    string
	Type    string
	Command string
	Args    []string
}

// backendRegistry 是內建 backend 加上 backends.json 覆蓋後的結果。
type backendRegistry struct {
	specs   map[string]backendSpec
	order   []string // 內建順序在前，其後是 config 新增的 backend（依名稱排序）
	cfgName string   // config 的 default；空字串表示未設定
}

func builtinBackendSpecs() []backendSpec {
	return []backendSpec{
		{Name: "claude", Type: backendTypeACP, Command: "claude-agent-acp"},
		{Name: "gemini", Type: backendTypeACP, Command: "gemini", Args: []string{"--acp"}},
		{Name: "codex", Type: backendTypeCodex},
	}
}

func newBuiltinRegistry() *backendRegistry {
	registry := &backendRegistry{specs: map[string]backendSpec{}}
	for _, spec := range builtinBackendSpecs() {
		registry.specs[spec.Name] = spec
		registry.order = append(registry.order, spec.Name)
	}
	return registry
}

// backendConfigPath 是唯一會被讀取的設定檔：${XDG_CONFIG_HOME:-~/.config}/lumen/backends.json。
// 相對的 XDG_CONFIG_HOME 依 XDG 規範視為無效，避免設定檔位置被導向 cwd / 專案目錄。
func backendConfigPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" || !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve config dir: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, appName, backendConfigFile), nil
}

// loadBackendRegistry 載入內建 registry，並套用使用者的 backends.json（若存在）。
// 所有錯誤都是設定錯誤，呼叫端應以 exit code 2 結束，且不得啟動任何 backend。
func loadBackendRegistry() (*backendRegistry, error) {
	path, err := backendConfigPath()
	if err != nil {
		return nil, err
	}
	return loadBackendRegistryFile(path)
}

func loadBackendRegistryFile(path string) (*backendRegistry, error) {
	registry := newBuiltinRegistry()
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return registry, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, unwrapPathError(err))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", path)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != backendConfigUID() {
		return nil, fmt.Errorf("%s: file is not owned by the current user", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("%s: file must not be group- or world-writable (mode %04o)", path, info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, unwrapPathError(err))
	}
	if err := registry.apply(data); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return registry, nil
}

func unwrapPathError(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

// apply 解析並驗證 backends.json。欄位白名單：default、backends.<name>.{type,command,args}；
// 其他 key（包含 env）一律拒絕。同名 entry 整筆取代內建。
func (r *backendRegistry) apply(data []byte) error {
	var top map[string]json.RawMessage
	if err := decodeStrictJSON(data, &top); err != nil {
		return fmt.Errorf("invalid JSON: %v", err)
	}
	for _, key := range sortedKeys(top) {
		if key != "default" && key != "backends" {
			return fmt.Errorf("unsupported key %q (allowed: default, backends)", key)
		}
	}
	var entries map[string]map[string]json.RawMessage
	if raw, ok := top["backends"]; ok {
		if err := decodeStrictJSON(raw, &entries); err != nil {
			return fmt.Errorf("backends must be an object of backend entries")
		}
	}
	var added []string
	for _, name := range sortedKeys(entries) {
		spec, err := parseBackendEntry(name, entries[name])
		if err != nil {
			return err
		}
		if _, exists := r.specs[name]; !exists {
			added = append(added, name)
		}
		r.specs[name] = spec
	}
	r.order = append(r.order, added...)
	if raw, ok := top["default"]; ok {
		var name string
		if err := decodeStrictJSON(raw, &name); err != nil || name == "" {
			return errors.New("default must be a non-empty backend name")
		}
		if _, exists := r.specs[name]; !exists {
			return fmt.Errorf("default backend %q is not configured (available: %s)", name, strings.Join(r.order, ", "))
		}
		r.cfgName = name
	}
	return nil
}

func parseBackendEntry(name string, fields map[string]json.RawMessage) (backendSpec, error) {
	if !backendNamePattern.MatchString(name) {
		return backendSpec{}, fmt.Errorf("invalid backend name %q (use letters, digits, '.', '_' or '-')", name)
	}
	for _, key := range sortedKeys(fields) {
		switch key {
		case "type", "command", "args":
		case "env":
			return backendSpec{}, fmt.Errorf("unsupported key %q: environment variables cannot be set in backends.json", "backends."+name+".env")
		default:
			return backendSpec{}, fmt.Errorf("unsupported key %q (allowed: type, command, args)", "backends."+name+"."+key)
		}
	}
	spec := backendSpec{Name: name}
	if raw, ok := fields["type"]; ok {
		if err := decodeStrictJSON(raw, &spec.Type); err != nil {
			return backendSpec{}, fmt.Errorf("backends.%s.type must be a string", name)
		}
	}
	if raw, ok := fields["command"]; ok {
		if err := decodeStrictJSON(raw, &spec.Command); err != nil {
			return backendSpec{}, fmt.Errorf("backends.%s.command must be a string", name)
		}
	}
	if raw, ok := fields["args"]; ok {
		if err := decodeStrictJSON(raw, &spec.Args); err != nil {
			return backendSpec{}, fmt.Errorf("backends.%s.args must be an array of strings", name)
		}
	}
	switch spec.Type {
	case backendTypeACP:
		if strings.TrimSpace(spec.Command) == "" {
			return backendSpec{}, fmt.Errorf("backends.%s: type acp requires command", name)
		}
	case backendTypeCodex:
		if spec.Command != "" || len(spec.Args) > 0 {
			return backendSpec{}, fmt.Errorf("backends.%s: type codex does not accept command or args", name)
		}
	default:
		return backendSpec{}, fmt.Errorf("backends.%s.type %q is not supported (use acp or codex)", name, spec.Type)
	}
	return spec, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after JSON value")
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Names 依序回傳可用的 backend 名稱。
func (r *backendRegistry) Names() []string {
	return append([]string(nil), r.order...)
}

func (r *backendRegistry) Spec(name string) (backendSpec, bool) {
	spec, ok := r.specs[name]
	return spec, ok
}

// Resolve 依 --backend > LUMEN_BACKEND > config default > claude 決定 backend。
func (r *backendRegistry) Resolve(flagName, envName string) (backendSpec, error) {
	name := strings.TrimSpace(flagName)
	if name == "" {
		name = strings.TrimSpace(envName)
	}
	if name == "" {
		name = r.cfgName
	}
	if name == "" {
		name = defaultBackendName
	}
	spec, ok := r.specs[name]
	if !ok {
		return backendSpec{}, fmt.Errorf("unknown backend %q (available: %s)", name, strings.Join(r.order, ", "))
	}
	return spec, nil
}

// isClaudeAdapter 判斷 spec 是否啟動 Claude 的 ACP adapter（包含使用者以 npx 明確 opt-in 的情況）。
func (s backendSpec) isClaudeAdapter() bool {
	if filepath.Base(s.Command) == "claude-agent-acp" {
		return true
	}
	for _, arg := range s.Args {
		if strings.Contains(arg, "claude-agent-acp") {
			return true
		}
	}
	return false
}

// acpConfig 把 spec 轉成 acpBackend 設定。Claude adapter 會帶 allowDangerouslySkipPermissions:false
// （只是限制，不是 mode 變更要求）；只有直接啟動 claude-agent-acp 時才附安裝指令。
func (s backendSpec) acpConfig() acpConfig {
	cfg := acpConfig{Name: s.Name, Command: s.Command, Args: append([]string(nil), s.Args...)}
	if s.isClaudeAdapter() {
		cfg.SessionMeta = claudeACPConfig().SessionMeta
	}
	if filepath.Base(s.Command) == "claude-agent-acp" {
		cfg.InstallHint = claudeACPInstallHint
	}
	return cfg
}

// newBackend 建立尚未 Open 的 backend。
func (s backendSpec) newBackend() (Backend, error) {
	switch s.Type {
	case backendTypeCodex:
		return startCodexBackend()
	case backendTypeACP:
		return newACPBackend(s.acpConfig()), nil
	}
	return nil, fmt.Errorf("backend %s: unsupported type %q", s.Name, s.Type)
}

// Factory 回傳建立指定 backend 的函式（供 reconnect supervisor 與 /backend 使用）。
func (r *backendRegistry) Factory(name string) func() (Backend, error) {
	spec, ok := r.specs[name]
	if !ok {
		return func() (Backend, error) { return nil, fmt.Errorf("unknown backend %q", name) }
	}
	return spec.newBackend
}
