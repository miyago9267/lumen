package lumen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeSlashCommandCatalogIncludesCodexCommands(t *testing.T) {
	for _, name := range []string{
		"model", "permissions", "skills", "review", "rename", "new", "archive", "delete",
		"resume", "fork", "compact", "plan", "goal", "copy", "diff", "status", "pwd",
		"usage", "statusline", "mcp", "plugins", "quit", "exit", "stop", "clear", "personality",
	} {
		if _, ok := slashCommandByName(name); !ok {
			t.Fatalf("slash command %q is missing from the catalog", name)
		}
	}
	for _, alias := range []string{"clean", "cwd", "pet", "q"} {
		if _, ok := slashCommandByName(alias); !ok {
			t.Fatalf("slash command alias %q is missing from the catalog", alias)
		}
	}
}

func TestSlashCommandFilterKeepsCanonicalOrder(t *testing.T) {
	commands := filterSlashCommands("st")
	if len(commands) < 3 {
		t.Fatalf("filtered slash commands = %#v, want status family", commands)
	}
	if commands[0].name != "status" || commands[1].name != "statusline" || commands[2].name != "stop" {
		t.Fatalf("filtered slash commands = %#v, want status, statusline, stop first", commands[:3])
	}
}

func TestSlashCommandCompletionMovesCursorToEnd(t *testing.T) {
	frontend := &ui{screenMode: true, input: []rune("/st"), cursor: 3}
	if !frontend.completeSlashCommand() {
		t.Fatal("slash command completion did not select a match")
	}
	if got := string(frontend.input); got != "/status" {
		t.Fatalf("completed input = %q, want /status", got)
	}
	if frontend.cursor != len(frontend.input) {
		t.Fatalf("completed cursor = %d, want %d", frontend.cursor, len(frontend.input))
	}
}

func TestCommandPopupTabCompletionAndArgumentBoundary(t *testing.T) {
	frontend := &ui{screenMode: true, input: []rune("/st"), cursor: 3}
	if err := frontend.handleKey(keyEvent{typ: keyTab}); err != nil {
		t.Fatalf("Tab completion error = %v", err)
	}
	if got := string(frontend.input); got != "/status" {
		t.Fatalf("Tab completed input = %q, want /status", got)
	}
	frontend.input = []rune("/status ")
	frontend.cursor = len(frontend.input)
	if frontend.commandPopupVisible() {
		t.Fatal("command popup remained visible after arguments started")
	}
	if plain := stripANSI(frontend.screenFrame(100, 24)); strings.Contains(plain, "Codex commands") {
		t.Fatalf("command popup rendered after arguments: %q", plain)
	}
}

func TestScreenCommandPopupShowsInputHints(t *testing.T) {
	frontend := &ui{screenMode: true, input: []rune("/st"), cursor: 3, cwd: "/tmp/project"}
	plain := stripANSI(frontend.screenFrame(100, 24))
	for _, expected := range []string{"/status", "show current session configuration", "Tab complete", "Esc close"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("command popup missing %q: %q", expected, plain)
		}
	}
}

func TestBottomStatusEmbedsCoralline(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project", thread: threadSummary{Model: "gpt-test"}}
	plain := stripANSI(frontend.screenFrame(100, 16))
	if !strings.Contains(plain, "coralline") {
		t.Fatalf("bottom status missing coralline: %q", plain)
	}
}

func TestRecognizedNativeCommandDoesNotBecomePrompt(t *testing.T) {
	var output bytes.Buffer
	frontend := &ui{
		screenMode: true,
		out:        &output,
		thread:     threadSummary{Model: "gpt-test", ApprovalPolicy: "on-request"},
		cwd:        "/tmp/project",
	}
	if err := frontend.handleCommand("/ide"); err != nil {
		t.Fatalf("recognized unsupported command error = %v", err)
	}
	plain := stripANSI(frontend.screenFrame(100, 16))
	if !strings.Contains(plain, "/ide") || strings.Contains(plain, "unknown command") {
		t.Fatalf("native command fallback was not explicit: %q", plain)
	}
}

func TestBusyNativeCommandDoesNotEnterPromptQueue(t *testing.T) {
	frontend := &ui{
		screenMode: true,
		busy:       true,
		input:      []rune("/status"),
		thread:     threadSummary{Model: "gpt-test"},
	}
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatalf("busy native command error = %v", err)
	}
	if len(frontend.queuedPrompts) != 0 {
		t.Fatalf("busy native command entered prompt queue: %#v", frontend.queuedPrompts)
	}
	if len(frontend.screenBlocks) == 0 || !strings.Contains(frontend.screenBlocks[len(frontend.screenBlocks)-1].text, "status") {
		t.Fatalf("busy native command produced no status output: %#v", frontend.screenBlocks)
	}
}

func TestPermissionsCommandUpdatesSessionOverride(t *testing.T) {
	frontend := &ui{screenMode: true, thread: threadSummary{ApprovalPolicy: "on-request"}}
	if err := frontend.handleCommand("/permissions workspace-write"); err != nil {
		t.Fatalf("permissions command error = %v", err)
	}
	if frontend.overrides.sandbox != "workspace-write" {
		t.Fatalf("sandbox override = %q, want workspace-write", frontend.overrides.sandbox)
	}
}

func TestReadKeysParsesTab(t *testing.T) {
	keys := make(chan keyEvent, 1)
	go readKeys(strings.NewReader("\t"), keys)
	key, ok := <-keys
	if !ok || key.typ != keyTab {
		t.Fatalf("readKeys tab = %#v, want keyTab", key)
	}
}

func TestReadKeysParsesCtrlU(t *testing.T) {
	keys := make(chan keyEvent, 1)
	go readKeys(strings.NewReader("\x15"), keys)
	key, ok := <-keys
	if !ok || key.typ != keyCtrlU {
		t.Fatalf("readKeys Ctrl+U = %#v, want keyCtrlU", key)
	}
}

func TestNativeCodexInvocationRecognizesSubcommands(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
		ok   bool
	}{
		{name: "direct subcommand", args: []string{"exec", "inspect"}, want: []string{"exec", "inspect"}, ok: true},
		{name: "global flags before subcommand", args: []string{"--model", "gpt-test", "review"}, want: []string{"--model", "gpt-test", "review"}, ok: true},
		{name: "boolean flags before subcommand", args: []string{"--worktree", "doctor"}, want: []string{"--worktree", "doctor"}, ok: true},
		{name: "long value with equals", args: []string{"--config=model=\"gpt-test\"", "features"}, want: []string{"--config=model=\"gpt-test\"", "features"}, ok: true},
		{name: "explicit codex prefix", args: []string{"codex", "mcp", "list"}, want: []string{"mcp", "list"}, ok: true},
		{name: "ordinary prompt", args: []string{"please", "inspect"}, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := nativeCodexInvocation(test.args)
			if ok != test.ok || strings.Join(got, "\x00") != strings.Join(test.want, "\x00") {
				t.Fatalf("nativeCodexInvocation(%v) = (%v, %t), want (%v, %t)", test.args, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestRunNativeCodexPropagatesExitCode(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "codex")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 7\n"), 0o700); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("LUMEN_CODEX_BIN", binary)
	if got := runNativeCodex([]string{"doctor"}); got != 7 {
		t.Fatalf("runNativeCodex exit code = %d, want 7", got)
	}
}
