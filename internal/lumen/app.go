package lumen

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

const (
	appName    = "lumen"
	appVersion = "0.3.1"
)

type options struct {
	resume     string
	continue_  bool
	fullscreen bool
	minimal    bool
	version    bool
	prompt     string
	runtime    runtimeOverrides
}

type runtimeOverrides struct {
	model           string
	reasoningEffort string
	approvalPolicy  string
	sandbox         string
}

func Run(args []string) int {
	if nativeArgs, ok := nativeCodexInvocation(args); ok {
		return runNativeCodex(nativeArgs)
	}
	opts, err := parseOptions(args)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", appName, err)
		return 2
	}
	if opts.version {
		fmt.Println(appName, appVersion)
		return 0
	}

	if opts.prompt == "" && !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "lumen: interactive terminal required")
		return 2
	}

	client, err := startAppServer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", appName, err)
		return 1
	}
	defer client.Close()

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: get cwd: %v\n", appName, err)
		return 1
	}

	if err := client.Initialize(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: initialize app-server: %v\n", appName, err)
		return 1
	}

	thread, err := openThread(client, cwd, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: open thread: %v\n", appName, err)
		return 1
	}

	threadCWD := thread.CWD
	if threadCWD == "" {
		threadCWD = cwd
	}
	ui := newUI(client, thread, threadCWD, opts.fullscreen, opts.runtime)
	if err := ui.run(opts.prompt); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", appName, err)
		return 1
	}
	return 0
}

func parseOptions(args []string) (options, error) {
	var opts options
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `Lumen terminal frontend

Usage:
  lumen [OPTIONS] [PROMPT]
  lumen <CODEX-SUBCOMMAND> [ARGS]
  lumen codex <CODEX-ARGS>

Native Codex subcommands are passed to the installed codex binary with their
original stdio and exit code. Interactive prompts use the local codex
app-server and do not replace Codex configuration.

Options:
`)
		fs.PrintDefaults()
	}
	fs.StringVar(&opts.resume, "resume", "", "resume a Codex thread by id")
	fs.BoolVar(&opts.continue_, "continue", false, "resume the newest thread for this directory")
	fs.BoolVar(&opts.fullscreen, "fullscreen", true, "use the full-screen app shell (default)")
	fs.BoolVar(&opts.minimal, "minimal", false, "use inline scrollback mode instead")
	fs.BoolVar(&opts.minimal, "no-alt-screen", false, "alias for --minimal")
	fs.StringVar(&opts.runtime.model, "model", "", "override the configured model for this session")
	fs.StringVar(&opts.runtime.reasoningEffort, "reasoning-effort", "", "override reasoning effort for turns")
	fs.StringVar(&opts.runtime.sandbox, "sandbox", "", "override sandbox: read-only, workspace-write, danger-full-access")
	fs.StringVar(&opts.runtime.approvalPolicy, "approval-policy", "", "override approval: untrusted, on-request, never")
	fs.StringVar(&opts.runtime.approvalPolicy, "ask-for-approval", "", "alias for --approval-policy")
	fs.BoolVar(&opts.version, "version", false, "print version")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if opts.resume != "" && opts.continue_ {
		return opts, errors.New("--resume and --continue cannot be combined")
	}
	if opts.minimal {
		opts.fullscreen = false
	}
	if err := validateRuntimeOverrides(opts.runtime); err != nil {
		return opts, err
	}
	opts.prompt = strings.TrimSpace(strings.Join(fs.Args(), " "))
	return opts, nil
}

func validateRuntimeOverrides(overrides runtimeOverrides) error {
	if overrides.sandbox != "" {
		switch overrides.sandbox {
		case "read-only", "workspace-write", "danger-full-access":
		default:
			return fmt.Errorf("unsupported sandbox %q", overrides.sandbox)
		}
	}
	if overrides.approvalPolicy != "" {
		switch overrides.approvalPolicy {
		case "untrusted", "on-request", "never":
		default:
			return fmt.Errorf("unsupported approval policy %q", overrides.approvalPolicy)
		}
	}
	return nil
}

type threadSummary struct {
	ID               string          `json:"id"`
	Model            string          `json:"model"`
	ReasoningEffort  string          `json:"reasoningEffort"`
	ApprovalPolicy   string          `json:"approvalPolicy"`
	Sandbox          json.RawMessage `json:"sandbox"`
	CWD              string          `json:"cwd"`
	Name             string          `json:"name"`
	Preview          string          `json:"preview"`
	Status           any             `json:"status"`
	UpdatedAt        any             `json:"updatedAt"`
	InstructionFiles []string        `json:"instructionSources"`
	Turns            []historyTurn   `json:"turns"`
}

type historyTurn struct {
	ID     string        `json:"id"`
	Status string        `json:"status"`
	Items  []historyItem `json:"items"`
}

type historyItem struct {
	Type             string         `json:"type"`
	Text             string         `json:"text"`
	Content          []historyInput `json:"content"`
	Command          string         `json:"command"`
	AggregatedOutput string         `json:"aggregatedOutput"`
	ExitCode         any            `json:"exitCode"`
	Status           string         `json:"status"`
	Server           string         `json:"server"`
	Tool             string         `json:"tool"`
	Summary          []string       `json:"summary"`
}

type historyInput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func openThread(client *appServer, cwd string, opts options) (threadSummary, error) {
	if opts.resume != "" {
		return client.ResumeThread(opts.resume, opts.runtime)
	}
	if opts.continue_ {
		threads, err := client.ListThreads(cwd, 1)
		if err != nil {
			return threadSummary{}, err
		}
		if len(threads) == 0 {
			return client.StartThread(cwd, opts.runtime)
		}
		return client.ResumeThread(threads[0].ID, opts.runtime)
	}
	return client.StartThread(cwd, opts.runtime)
}

func startAppServer() (*appServer, error) {
	bin, err := codexBinary()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, appServerArgs()...)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open app-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open app-server stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start app-server: %w", err)
	}
	client := newAppServer(stdin, stdout, cmd)
	go client.readLoop()
	return client, nil
}

func appServerArgs() []string {
	return []string{
		"app-server",
		"--listen",
		"stdio://",
		"--config",
		"suppress_unstable_features_warning=true",
	}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type pendingCall struct {
	result chan rpcMessage
}

type serverRequest struct {
	id     json.RawMessage
	method string
	params json.RawMessage
}

type appServer struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmd    *exec.Cmd

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[string]pendingCall

	notifications chan rpcMessage
	requests      chan serverRequest
	errors        chan error
	closed        chan struct{}
	closeOnce     sync.Once
	failure       error
}

func newAppServer(stdin io.WriteCloser, stdout io.ReadCloser, cmd *exec.Cmd) *appServer {
	return &appServer{
		stdin:         stdin,
		stdout:        stdout,
		cmd:           cmd,
		nextID:        1,
		pending:       make(map[string]pendingCall),
		notifications: make(chan rpcMessage, 256),
		requests:      make(chan serverRequest, 64),
		errors:        make(chan error, 1),
		closed:        make(chan struct{}),
	}
}

func (c *appServer) readLoop() {
	decoder := json.NewDecoder(c.stdout)
	for {
		var message rpcMessage
		if err := decoder.Decode(&message); err != nil {
			if !errors.Is(err, io.EOF) {
				c.closeWithError(fmt.Errorf("read app-server: %w", err))
			} else {
				c.closeWithError(nil)
			}
			return
		}

		if len(message.ID) > 0 && (len(message.Result) > 0 || message.Error != nil) {
			key := string(message.ID)
			c.mu.Lock()
			call, ok := c.pending[key]
			if ok {
				delete(c.pending, key)
			}
			c.mu.Unlock()
			if ok {
				call.result <- message
			}
			continue
		}
		if len(message.ID) > 0 && message.Method != "" {
			select {
			case c.requests <- serverRequest{message.ID, message.Method, message.Params}:
			case <-c.closed:
				return
			}
			continue
		}
		select {
		case c.notifications <- message:
		case <-c.closed:
			return
		}
	}
}

func (c *appServer) closeWithError(err error) {
	if err != nil {
		c.mu.Lock()
		if c.failure == nil {
			c.failure = err
		}
		c.mu.Unlock()
		select {
		case c.errors <- err:
		default:
		}
	}
	c.closeOnce.Do(func() { close(c.closed) })
}

func (c *appServer) failureError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failure
}

func (c *appServer) Initialize() error {
	var result struct {
		UserAgent string `json:"userAgent"`
		CodexHome string `json:"codexHome"`
	}
	if err := c.Call("initialize", map[string]any{
		"clientInfo": map[string]string{
			"name":    appName,
			"title":   "Lumen terminal UI",
			"version": appVersion,
		},
	}, &result); err != nil {
		return err
	}
	return c.notify("initialized", map[string]any{})
}

func (c *appServer) StartThread(cwd string, overrides runtimeOverrides) (threadSummary, error) {
	var result struct {
		Thread          threadSummary   `json:"thread"`
		Model           string          `json:"model"`
		CWD             string          `json:"cwd"`
		Instructions    []string        `json:"instructionSources"`
		ApprovalPolicy  json.RawMessage `json:"approvalPolicy"`
		Sandbox         json.RawMessage `json:"sandbox"`
		ReasoningEffort string          `json:"reasoningEffort"`
	}
	params := map[string]any{
		"cwd":         cwd,
		"serviceName": appName,
	}
	applyThreadOverrides(params, overrides)
	if err := c.Call("thread/start", params, &result); err != nil {
		return threadSummary{}, err
	}
	applyThreadMetadata(&result.Thread, result.Model, result.CWD, result.Instructions, result.ApprovalPolicy)
	result.Thread.Sandbox = result.Sandbox
	result.Thread.ReasoningEffort = result.ReasoningEffort
	return result.Thread, nil
}

func (c *appServer) ResumeThread(id string, overrides runtimeOverrides) (threadSummary, error) {
	var result struct {
		Thread          threadSummary   `json:"thread"`
		Model           string          `json:"model"`
		CWD             string          `json:"cwd"`
		Instructions    []string        `json:"instructionSources"`
		ApprovalPolicy  json.RawMessage `json:"approvalPolicy"`
		Sandbox         json.RawMessage `json:"sandbox"`
		ReasoningEffort string          `json:"reasoningEffort"`
	}
	params := map[string]any{"threadId": id}
	applyThreadOverrides(params, overrides)
	if err := c.Call("thread/resume", params, &result); err != nil {
		return threadSummary{}, err
	}
	applyThreadMetadata(&result.Thread, result.Model, result.CWD, result.Instructions, result.ApprovalPolicy)
	result.Thread.Sandbox = result.Sandbox
	result.Thread.ReasoningEffort = result.ReasoningEffort
	return result.Thread, nil
}

func (c *appServer) ListThreads(cwd string, limit int) ([]threadSummary, error) {
	if limit < 1 {
		limit = 1
	}
	var result struct {
		Data []threadSummary `json:"data"`
	}
	params := map[string]any{
		"limit":    limit,
		"sortKey":  "updated_at",
		"archived": false,
	}
	if strings.TrimSpace(cwd) != "" {
		params["cwd"] = cwd
	}
	if err := c.Call("thread/list", params, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

type codexModel struct {
	ID          string `json:"id"`
	Model       string `json:"model"`
	DisplayName string `json:"displayName"`
	Hidden      bool   `json:"hidden"`
}

func (c *appServer) ListModels() ([]codexModel, error) {
	var result struct {
		Data []codexModel `json:"data"`
	}
	if err := c.Call("model/list", map[string]any{
		"includeHidden": false,
		"limit":         50,
	}, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

type codexSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type codexSkillEntry struct {
	CWD    string       `json:"cwd"`
	Skills []codexSkill `json:"skills"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type codexHookEntry struct {
	CWD   string `json:"cwd"`
	Hooks []struct {
		Key        string `json:"key"`
		EventName  string `json:"eventName"`
		Enabled    bool   `json:"enabled"`
		StatusText string `json:"statusMessage"`
	} `json:"hooks"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (c *appServer) ListSkills(cwd string) ([]codexSkillEntry, error) {
	var result struct {
		Data []codexSkillEntry `json:"data"`
	}
	if err := c.Call("skills/list", map[string]any{
		"cwds":        []string{cwd},
		"forceReload": false,
	}, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

func (c *appServer) ListHooks(cwd string) ([]codexHookEntry, error) {
	var result struct {
		Data []codexHookEntry `json:"data"`
	}
	if err := c.Call("hooks/list", map[string]any{
		"cwds": []string{cwd},
	}, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

type codexMCPServer struct {
	Name          string         `json:"name"`
	Tools         map[string]any `json:"tools"`
	ToolsError    *string        `json:"toolsError"`
	RuntimeStatus any            `json:"runtimeStatus"`
}

func (c *appServer) ListMCPServers(threadID string) ([]codexMCPServer, error) {
	var result struct {
		Data []codexMCPServer `json:"data"`
	}
	if err := c.Call("mcpServerStatus/list", map[string]any{
		"threadId": threadID,
		"limit":    50,
	}, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

type codexApp struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"isEnabled"`
}

type codexPluginMarketplace struct {
	Name    string `json:"name"`
	Plugins []struct {
		Name      string `json:"name"`
		Installed bool   `json:"installed"`
		Enabled   bool   `json:"enabled"`
	} `json:"plugins"`
}

func (c *appServer) ListApps() ([]codexApp, error) {
	var result struct {
		Data []codexApp `json:"data"`
	}
	if err := c.Call("app/list", map[string]any{
		"limit": 50,
	}, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

func (c *appServer) ListPlugins(cwd string) ([]codexPluginMarketplace, error) {
	var result struct {
		Marketplaces []codexPluginMarketplace `json:"marketplaces"`
	}
	if err := c.Call("plugin/list", map[string]any{
		"cwds":         []string{cwd},
		"forceRefetch": false,
	}, &result); err != nil {
		return nil, err
	}
	return result.Marketplaces, nil
}

func (c *appServer) ForkThread(id string, overrides runtimeOverrides) (threadSummary, error) {
	return c.forkThread(id, "", overrides)
}

func (c *appServer) ForkThreadAt(id, lastTurnID string, overrides runtimeOverrides) (threadSummary, error) {
	return c.forkThread(id, lastTurnID, overrides)
}

func (c *appServer) forkThread(id, lastTurnID string, overrides runtimeOverrides) (threadSummary, error) {
	var result struct {
		Thread          threadSummary   `json:"thread"`
		Model           string          `json:"model"`
		CWD             string          `json:"cwd"`
		Instructions    []string        `json:"instructionSources"`
		ApprovalPolicy  json.RawMessage `json:"approvalPolicy"`
		Sandbox         json.RawMessage `json:"sandbox"`
		ReasoningEffort string          `json:"reasoningEffort"`
	}
	params := map[string]any{"threadId": id}
	if lastTurnID != "" {
		params["lastTurnId"] = lastTurnID
	}
	applyThreadOverrides(params, overrides)
	if err := c.Call("thread/fork", params, &result); err != nil {
		return threadSummary{}, err
	}
	applyThreadMetadata(&result.Thread, result.Model, result.CWD, result.Instructions, result.ApprovalPolicy)
	result.Thread.Sandbox = result.Sandbox
	result.Thread.ReasoningEffort = result.ReasoningEffort
	return result.Thread, nil
}

func (c *appServer) ArchiveThread(id string) error {
	return c.Call("thread/archive", map[string]string{"threadId": id}, nil)
}

func (c *appServer) DeleteThread(id string) error {
	return c.Call("thread/delete", map[string]string{"threadId": id}, nil)
}

func (c *appServer) SetThreadName(id, name string) error {
	return c.Call("thread/name/set", map[string]string{"threadId": id, "name": name}, nil)
}

func (c *appServer) CompactThread(id string) error {
	return c.Call("thread/compact/start", map[string]string{"threadId": id}, nil)
}

func (c *appServer) StartReview(threadID string, target map[string]any) (string, error) {
	var result struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := c.Call("review/start", map[string]any{
		"threadId": threadID,
		"target":   target,
		"delivery": "inline",
	}, &result); err != nil {
		return "", err
	}
	return result.Turn.ID, nil
}

func (c *appServer) GetThreadGoal(id string) (map[string]any, error) {
	var result struct {
		Goal map[string]any `json:"goal"`
	}
	if err := c.Call("thread/goal/get", map[string]string{"threadId": id}, &result); err != nil {
		return nil, err
	}
	return result.Goal, nil
}

func (c *appServer) SetThreadGoal(id, objective string) (map[string]any, error) {
	var result struct {
		Goal map[string]any `json:"goal"`
	}
	if err := c.Call("thread/goal/set", map[string]any{
		"threadId":  id,
		"objective": objective,
	}, &result); err != nil {
		return nil, err
	}
	return result.Goal, nil
}

func (c *appServer) ClearThreadGoal(id string) error {
	return c.Call("thread/goal/clear", map[string]string{"threadId": id}, nil)
}

func applyThreadMetadata(thread *threadSummary, model, cwd string, instructions []string, approvalPolicy json.RawMessage) {
	if model != "" {
		thread.Model = model
	}
	if cwd != "" {
		thread.CWD = cwd
	}
	if len(instructions) > 0 {
		thread.InstructionFiles = instructions
	}
	if len(approvalPolicy) > 0 {
		thread.ApprovalPolicy = displayApprovalPolicy(approvalPolicy)
	}
}

func (c *appServer) StartTurn(threadID, text string, overrides runtimeOverrides) (string, error) {
	var result struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	params := map[string]any{
		"threadId": threadID,
		"input": []map[string]string{{
			"type": "text",
			"text": text,
		}},
	}
	applyTurnOverrides(params, overrides)
	if err := c.Call("turn/start", params, &result); err != nil {
		return "", err
	}
	return result.Turn.ID, nil
}

func applyThreadOverrides(params map[string]any, overrides runtimeOverrides) {
	if overrides.model != "" {
		params["model"] = overrides.model
	}
	if overrides.approvalPolicy != "" {
		params["approvalPolicy"] = overrides.approvalPolicy
	}
	if overrides.sandbox != "" {
		params["sandbox"] = overrides.sandbox
	}
}

func applyTurnOverrides(params map[string]any, overrides runtimeOverrides) {
	if overrides.model != "" {
		params["model"] = overrides.model
	}
	if overrides.reasoningEffort != "" {
		params["effort"] = overrides.reasoningEffort
	}
	if overrides.approvalPolicy != "" {
		params["approvalPolicy"] = overrides.approvalPolicy
	}
}

func (c *appServer) Interrupt(threadID, turnID string) error {
	return c.Call("turn/interrupt", map[string]string{
		"threadId": threadID,
		"turnId":   turnID,
	}, nil)
}

func (c *appServer) Call(method string, params any, target any) error {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	rawID := json.RawMessage(fmt.Sprintf("%d", id))
	result := make(chan rpcMessage, 1)
	c.pending[string(rawID)] = pendingCall{result: result}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, string(rawID))
		c.mu.Unlock()
	}()

	if err := c.writeMessage(map[string]any{
		"method": method,
		"id":     id,
		"params": params,
	}); err != nil {
		return err
	}

	select {
	case message := <-result:
		if message.Error != nil {
			return fmt.Errorf("%s (%d): %s", method, message.Error.Code, message.Error.Message)
		}
		if target == nil || len(message.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(message.Result, target); err != nil {
			return fmt.Errorf("decode %s response: %w", method, err)
		}
		return nil
	case err := <-c.errors:
		return err
	case <-c.closed:
		if err := c.failureError(); err != nil {
			return err
		}
		return fmt.Errorf("app-server closed before %s completed", method)
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timeout waiting for %s", method)
	}
}

func (c *appServer) notify(method string, params any) error {
	return c.writeMessage(map[string]any{"method": method, "params": params})
}

func (c *appServer) respond(id json.RawMessage, result any) error {
	var raw json.RawMessage
	if result != nil {
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		raw = encoded
	} else {
		raw = json.RawMessage(`{}`)
	}
	return c.writeMessage(map[string]any{
		"id":     id,
		"result": raw,
	})
}

func (c *appServer) respondError(id json.RawMessage, code int, message string) error {
	return c.writeMessage(map[string]any{
		"id": id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func (c *appServer) writeMessage(message any) error {
	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if c.stdin == nil {
		return errors.New("app-server stdin unavailable")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.closed:
		return errors.New("app-server closed")
	default:
	}
	if _, err := c.stdin.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write app-server: %w", err)
	}
	return nil
}

func (c *appServer) Close() {
	c.closeWithError(nil)
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.stdout != nil {
		_ = c.stdout.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

func sandboxLabel(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "inherit"
	}
	var value struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &value) == nil && value.Type != "" {
		switch value.Type {
		case "dangerFullAccess":
			return "danger-full-access"
		case "readOnly":
			return "read-only"
		case "workspaceWrite":
			return "workspace-write"
		default:
			return value.Type
		}
	}
	return strings.Trim(string(raw), `"`)
}

func displayApprovalPolicy(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var granular struct {
		Granular map[string]bool `json:"granular"`
	}
	if json.Unmarshal(raw, &granular) == nil && granular.Granular != nil {
		return "granular"
	}
	return strings.TrimSpace(string(raw))
}

func displayCWD(cwd string) string {
	home, err := os.UserHomeDir()
	if err == nil && (cwd == home || strings.HasPrefix(cwd, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(cwd, home)
	}
	return cwd
}
