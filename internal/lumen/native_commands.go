package lumen

import (
	"fmt"
	"strings"
)

const statusBarBrand = "carolline"

type slashCommand struct {
	name               string
	description        string
	aliases            []string
	supportsInlineArgs bool
}

// Keep this catalog in the same order as the stable Codex TUI command list.
// Lumen-only commands are kept at the end so native command completion stays
// predictable.
var slashCommandCatalog = []slashCommand{
	{name: "model", description: "choose or show the current model"},
	{name: "ide", description: "connect to the current IDE", supportsInlineArgs: true},
	{name: "permissions", description: "change approval and permission mode"},
	{name: "keymap", description: "show or change key bindings", supportsInlineArgs: true},
	{name: "vim", description: "toggle vim editing mode"},
	{name: "setup-default-sandbox", description: "configure the default sandbox"},
	{name: "experimental", description: "configure experimental features"},
	{name: "approve", description: "approve a pending action"},
	{name: "memories", description: "show or manage memories"},
	{name: "skills", description: "show available skills"},
	{name: "import", description: "import a transcript or session"},
	{name: "hooks", description: "show configured hooks"},
	{name: "review", description: "start a code review", supportsInlineArgs: true},
	{name: "rename", description: "rename the current thread", supportsInlineArgs: true},
	{name: "new", description: "start a new thread", supportsInlineArgs: true},
	{name: "archive", description: "archive the current thread"},
	{name: "delete", description: "delete the current thread"},
	{name: "resume", description: "resume a thread", supportsInlineArgs: true},
	{name: "fork", description: "fork the current thread", supportsInlineArgs: true},
	{name: "worktree", description: "create or switch worktrees", supportsInlineArgs: true},
	{name: "app", description: "open the Codex app", supportsInlineArgs: true},
	{name: "init", description: "create project guidance files"},
	{name: "compact", description: "compact the current conversation"},
	{name: "plan", description: "enter or show the implementation plan", supportsInlineArgs: true},
	{name: "goal", description: "set or show the current goal", supportsInlineArgs: true},
	{name: "agent", description: "manage the current agent", supportsInlineArgs: true},
	{name: "side", description: "start a side conversation", supportsInlineArgs: true},
	{name: "btw", description: "ask a brief side question", supportsInlineArgs: true},
	{name: "copy", description: "copy the latest assistant response"},
	{name: "export", description: "export the current transcript"},
	{name: "raw", description: "show raw event details", supportsInlineArgs: true},
	{name: "diff", description: "show the current git diff"},
	{name: "mention", description: "mention a file or folder", supportsInlineArgs: true},
	{name: "status", description: "show current session configuration"},
	{name: "pwd", description: "show the current working directory", aliases: []string{"cwd"}},
	{name: "usage", description: "show session usage"},
	{name: "debug-config", description: "show effective debug configuration"},
	{name: "title", description: "set the thread title", supportsInlineArgs: true},
	{name: "statusline", description: "configure the status line", supportsInlineArgs: true},
	{name: "theme", description: "choose the terminal theme", supportsInlineArgs: true},
	{name: "pets", description: "show terminal pets", aliases: []string{"pet"}},
	{name: "mcp", description: "show MCP servers", supportsInlineArgs: true},
	{name: "apps", description: "show connected apps"},
	{name: "plugins", description: "show installed plugins"},
	{name: "logout", description: "log out of Codex"},
	{name: "quit", description: "exit without changing the official Codex CLI", aliases: []string{"exit", "q"}},
	{name: "feedback", description: "send feedback", supportsInlineArgs: true},
	{name: "ps", description: "show running background tasks"},
	{name: "stop", description: "stop background tasks", aliases: []string{"clean"}},
	{name: "clear", description: "clear the current transcript", supportsInlineArgs: true},
	{name: "personality", description: "choose the assistant personality", supportsInlineArgs: true},
	{name: "subagents", description: "show or manage subagents", supportsInlineArgs: true},
	{name: "help", description: "show available commands"},
	{name: "dashboard", description: "supervise top-level sessions"},
}

func slashCommandByName(name string) (slashCommand, bool) {
	name = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(name), "/"))
	if fields := strings.Fields(name); len(fields) > 0 {
		name = fields[0]
	}
	for _, command := range slashCommandCatalog {
		if command.name == name {
			return command, true
		}
		for _, alias := range command.aliases {
			if alias == name {
				return command, true
			}
		}
	}
	return slashCommand{}, false
}

func filterSlashCommands(query string) []slashCommand {
	query = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(query), "/"))
	if fields := strings.Fields(query); len(fields) > 0 {
		query = fields[0]
	}
	commands := make([]slashCommand, 0, len(slashCommandCatalog))
	if query == "" {
		return append(commands, slashCommandCatalog...)
	}
	for _, command := range slashCommandCatalog {
		if strings.HasPrefix(command.name, query) {
			commands = append(commands, command)
		}
	}
	if len(commands) > 0 {
		return commands
	}
	for _, command := range slashCommandCatalog {
		if isSubsequence(query, command.name) {
			commands = append(commands, command)
		}
	}
	return commands
}

func isSubsequence(query, value string) bool {
	if query == "" {
		return true
	}
	queryRunes := []rune(query)
	valueRunes := []rune(value)
	position := 0
	for _, character := range valueRunes {
		if character == queryRunes[position] {
			position++
			if position == len(queryRunes) {
				return true
			}
		}
	}
	return false
}

func slashCommandInput(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\r\n") {
		return "", "", false
	}
	rest := strings.TrimPrefix(value, "/")
	if rest == "" {
		return "", "", true
	}
	end := strings.IndexAny(rest, " \t")
	if end < 0 {
		return strings.ToLower(rest), "", true
	}
	return strings.ToLower(rest[:end]), strings.TrimSpace(rest[end:]), true
}

func (u *ui) commandPopupVisible() bool {
	if u.commandPopupDismissed || u.approval != nil || u.inputRequest != nil || u.dashboard || u.shortcuts {
		return false
	}
	value := string(u.input)
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(strings.TrimPrefix(value, "/"), " \t\r\n") {
		return false
	}
	query, args, ok := slashCommandInput(string(u.input))
	if !ok || args != "" {
		return false
	}
	return len(filterSlashCommands(query)) > 0
}

func (u *ui) commandPopupItems() []slashCommand {
	query, _, ok := slashCommandInput(string(u.input))
	if !ok {
		return nil
	}
	return filterSlashCommands(query)
}

func (u *ui) selectedSlashCommand() (slashCommand, bool) {
	items := u.commandPopupItems()
	if len(items) == 0 {
		return slashCommand{}, false
	}
	if u.commandPopupIndex < 0 {
		u.commandPopupIndex = 0
	}
	if u.commandPopupIndex >= len(items) {
		u.commandPopupIndex = len(items) - 1
	}
	return items[u.commandPopupIndex], true
}

func (u *ui) moveSlashCommandSelection(direction int) bool {
	if !u.commandPopupVisible() {
		return false
	}
	items := u.commandPopupItems()
	u.commandPopupIndex += direction
	if u.commandPopupIndex < 0 {
		u.commandPopupIndex = 0
	}
	if u.commandPopupIndex >= len(items) {
		u.commandPopupIndex = len(items) - 1
	}
	return true
}

func (u *ui) completeSlashCommand() bool {
	if !u.commandPopupVisible() {
		return false
	}
	command, ok := u.selectedSlashCommand()
	if !ok {
		return false
	}
	value := "/" + command.name
	if command.supportsInlineArgs {
		value += " "
	}
	u.input = []rune(value)
	u.cursor = len(u.input)
	u.commandPopupIndex = 0
	u.commandPopupDismissed = false
	u.renderPrompt()
	return true
}

func (u *ui) handleCommandPopupKey(key keyEvent) (bool, error) {
	if !u.commandPopupVisible() {
		return false, nil
	}
	switch key.typ {
	case keyUp:
		return true, func() error { u.moveSlashCommandSelection(-1); return nil }()
	case keyDown:
		return true, func() error { u.moveSlashCommandSelection(1); return nil }()
	case keyTab:
		return true, func() error { u.completeSlashCommand(); return nil }()
	case keyEscape:
		u.commandPopupDismissed = true
		u.renderPrompt()
		return true, nil
	case keyEnter:
		query, _, _ := slashCommandInput(string(u.input))
		if _, ok := slashCommandByName(query); !ok {
			if !u.completeSlashCommand() {
				return true, nil
			}
			return true, u.handleCommand(string(u.input))
		}
	}
	return false, nil
}

func (u *ui) screenCommandPopupLines(width int) []string {
	if !u.commandPopupVisible() {
		return nil
	}
	items := u.commandPopupItems()
	if len(items) == 0 {
		return nil
	}
	lines := []string{screenComposerLine("⌘ Codex commands", width, screenInfo)}
	selected := u.commandPopupIndex
	if selected < 0 {
		selected = 0
	}
	if selected >= len(items) {
		selected = len(items) - 1
	}
	for index, command := range items {
		if index == 6 {
			break
		}
		marker := "  "
		kind := screenStatus
		if index == selected {
			marker = "› "
			kind = screenUser
		}
		line := fmt.Sprintf("%s/%-18s  %s", marker, command.name, command.description)
		lines = append(lines, screenComposerLine(line, width, kind))
	}
	lines = append(lines, screenComposerLine("↑↓ choose · Tab complete · Enter run · Esc close", width, screenInfo))
	return lines
}

func (u *ui) inlineCommandPopupLines() int {
	if !u.commandPopupVisible() {
		return 0
	}
	items := u.commandPopupItems()
	count := len(items)
	if count > 6 {
		count = 6
	}
	return 2 + count
}
