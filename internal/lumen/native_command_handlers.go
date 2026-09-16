package lumen

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

func slashCommandHelpText() string {
	lines := []string{"commands"}
	for _, command := range slashCommandCatalog {
		aliases := ""
		if len(command.aliases) > 0 {
			aliases = " (" + strings.Join(command.aliases, ", ") + ")"
		}
		lines = append(lines, fmt.Sprintf("  /%-20s %s%s", command.name, command.description, aliases))
	}
	lines = append(lines, "  exit                  exit without changing the official Codex CLI")
	return strings.Join(lines, "\n")
}

func (u *ui) handleNativeCommand(command slashCommand, args string) error {
	switch command.name {
	case "model":
		return u.commandModel(args)
	case "permissions":
		return u.commandPermissions(args)
	case "skills":
		return u.commandSkills()
	case "hooks":
		return u.commandHooks()
	case "review":
		return u.commandReview(args)
	case "rename", "title":
		return u.commandRename(args)
	case "new":
		return u.commandNew(args)
	case "archive":
		return u.commandArchive()
	case "delete":
		return u.commandDelete(args)
	case "resume":
		return u.commandResume(args)
	case "fork":
		return u.commandFork()
	case "compact":
		return u.commandCompact()
	case "goal":
		return u.commandGoal(args)
	case "copy":
		return u.commandCopy()
	case "diff":
		return u.commandDiff()
	case "pwd":
		u.commandMessage(u.cwd, cyan)
		return nil
	case "usage":
		usage := u.usageLabel()
		if usage == "" {
			usage = "no token usage reported yet"
		}
		u.commandMessage("usage · "+usage, cyan)
		return nil
	case "statusline":
		u.commandMessage("status line · "+u.screenStatusLabel(), cyan)
		return nil
	case "clear":
		u.endScreenTurn()
		u.clearReplyTarget()
		u.clearScreenTranscript()
		u.clearSelection()
		u.scrollOffset = 0
		u.hoverText = ""
		u.showWelcome = false
		u.commandMessage("transcript cleared", cyan)
		return nil
	case "stop":
		return u.commandStop()
	case "mcp":
		return u.commandMCP()
	case "apps":
		return u.commandApps()
	case "debug-config":
		return u.commandDebugConfig()
	case "plugins":
		return u.commandPlugins()
	default:
		u.nativeCommandUnavailable(command.name)
		return nil
	}
}

func (u *ui) commandModel(args string) error {
	args = strings.TrimSpace(args)
	if args != "" && !strings.EqualFold(args, "list") {
		u.overrides.model = strings.Fields(args)[0]
		u.commandMessage("model override · "+u.overrides.model, green)
		return nil
	}
	if u.client == nil {
		u.commandMessage("model · "+u.effectiveModel(), cyan)
		return nil
	}
	models, err := u.client.ListModels()
	if err != nil {
		return err
	}
	lines := make([]string, 0, len(models))
	for _, model := range models {
		name := valueOr(model.DisplayName, valueOr(model.Model, model.ID))
		if model.Hidden {
			name += " · hidden"
		}
		lines = append(lines, name)
	}
	if len(lines) == 0 {
		lines = append(lines, "no models returned")
	}
	u.showCommandOutput("models", lines)
	return nil
}

func (u *ui) commandPermissions(args string) error {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 {
		u.showCommandOutput("permissions", []string{
			"approval: " + u.effectiveApproval(),
			"sandbox: " + u.effectiveSandbox(),
		})
		return nil
	}
	if len(fields) == 2 && strings.EqualFold(fields[0], "approval") {
		fields = fields[1:]
	}
	value := strings.ToLower(fields[0])
	switch value {
	case "untrusted", "on-request", "never":
		u.overrides.approvalPolicy = value
	case "read-only", "workspace-write", "danger-full-access":
		u.overrides.sandbox = value
	default:
		return fmt.Errorf("permissions expects approval (untrusted, on-request, never) or sandbox (read-only, workspace-write, danger-full-access)")
	}
	u.commandMessage("permissions updated · "+value, green)
	return nil
}

func (u *ui) commandSkills() error {
	if u.client == nil {
		u.commandMessage("skills requires app-server", yellow)
		return nil
	}
	entries, err := u.client.ListSkills(u.cwd)
	if err != nil {
		return err
	}
	lines := make([]string, 0)
	for _, entry := range entries {
		for _, skill := range entry.Skills {
			state := "enabled"
			if !skill.Enabled {
				state = "disabled"
			}
			lines = append(lines, fmt.Sprintf("%s · %s", skill.Name, state))
		}
		for _, skillError := range entry.Errors {
			lines = append(lines, "error · "+skillError.Message)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "no skills found")
	}
	u.showCommandOutput("skills", lines)
	return nil
}

func (u *ui) commandHooks() error {
	if u.client == nil {
		u.commandMessage("hooks requires app-server", yellow)
		return nil
	}
	entries, err := u.client.ListHooks(u.cwd)
	if err != nil {
		return err
	}
	lines := make([]string, 0)
	for _, entry := range entries {
		for _, hook := range entry.Hooks {
			state := "disabled"
			if hook.Enabled {
				state = "enabled"
			}
			name := valueOr(hook.Key, hook.EventName)
			lines = append(lines, fmt.Sprintf("%s · %s", name, state))
		}
		for _, hookError := range entry.Errors {
			lines = append(lines, "error · "+hookError.Message)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "no hooks found")
	}
	u.showCommandOutput("hooks", lines)
	return nil
}

func (u *ui) commandReview(args string) error {
	if u.busy {
		return fmt.Errorf("review cannot start while a turn is running")
	}
	if u.client == nil {
		return fmt.Errorf("review requires app-server")
	}
	target := map[string]any{"type": "uncommittedChanges"}
	args = strings.TrimSpace(args)
	fields := strings.Fields(args)
	switch {
	case len(fields) >= 2 && strings.EqualFold(fields[0], "base"):
		target = map[string]any{"type": "baseBranch", "branch": fields[1]}
	case len(fields) >= 2 && strings.EqualFold(fields[0], "commit"):
		target = map[string]any{"type": "commit", "sha": fields[1]}
		if len(fields) > 2 {
			target["title"] = strings.Join(fields[2:], " ")
		}
	case args != "":
		target = map[string]any{"type": "custom", "instructions": args}
	}
	turnID, err := u.client.StartReview(u.thread.ID, target)
	if err != nil {
		return err
	}
	u.beginCommandTurn(turnID)
	u.commandMessage("review started", cyan)
	u.renderBusy()
	return nil
}

func (u *ui) commandRename(args string) error {
	name := strings.TrimSpace(args)
	if name == "" {
		return fmt.Errorf("rename expects a thread name")
	}
	if u.client == nil {
		u.thread.Name = name
		u.commandMessage("thread renamed · "+name, green)
		return nil
	}
	if err := u.client.SetThreadName(u.thread.ID, name); err != nil {
		return err
	}
	u.thread.Name = name
	u.commandMessage("thread renamed · "+name, green)
	return nil
}

func (u *ui) commandNew(args string) error {
	if u.busy {
		return fmt.Errorf("new thread cannot start while a turn is running")
	}
	if u.client == nil {
		return fmt.Errorf("new thread requires app-server")
	}
	thread, err := u.client.StartThread(u.cwd, u.overrides)
	if err != nil {
		return err
	}
	u.adoptCommandThread(thread)
	if strings.TrimSpace(args) != "" {
		return u.submit(strings.TrimSpace(args))
	}
	u.commandMessage("new thread · "+shortID(thread.ID), green)
	return nil
}

func (u *ui) commandArchive() error {
	if u.busy {
		return fmt.Errorf("archive cannot run while a turn is running")
	}
	if u.client == nil {
		return fmt.Errorf("archive requires app-server")
	}
	if err := u.client.ArchiveThread(u.thread.ID); err != nil {
		return err
	}
	u.commandMessage("thread archived · "+shortID(u.thread.ID), green)
	return nil
}

func (u *ui) commandDelete(args string) error {
	if !strings.EqualFold(strings.TrimSpace(args), "confirm") {
		u.commandMessage("delete is destructive · type /delete confirm to continue", yellow)
		return nil
	}
	if u.busy {
		return fmt.Errorf("delete cannot run while a turn is running")
	}
	if u.client == nil {
		return fmt.Errorf("delete requires app-server")
	}
	if err := u.client.DeleteThread(u.thread.ID); err != nil {
		return err
	}
	return errQuit
}

func (u *ui) commandResume(args string) error {
	threadID := strings.TrimSpace(args)
	if threadID == "" {
		if u.screenMode {
			return u.openDashboard()
		}
		return fmt.Errorf("resume expects a thread id outside full-screen mode")
	}
	if u.busy {
		return fmt.Errorf("resume cannot run while a turn is running")
	}
	if u.client == nil {
		return fmt.Errorf("resume requires app-server")
	}
	thread, err := u.client.ResumeThread(strings.Fields(threadID)[0], u.overrides)
	if err != nil {
		return err
	}
	u.adoptCommandThread(thread)
	u.commandMessage("resumed · "+shortID(thread.ID), green)
	return nil
}

func (u *ui) commandFork() error {
	if u.busy {
		return fmt.Errorf("fork cannot run while a turn is running")
	}
	if u.client == nil {
		return fmt.Errorf("fork requires app-server")
	}
	thread, err := u.client.ForkThread(u.thread.ID, u.overrides)
	if err != nil {
		return err
	}
	u.adoptCommandThread(thread)
	u.commandMessage("forked · "+shortID(thread.ID), green)
	return nil
}

func (u *ui) handleScreenTurnAction(action screenTurnAction, target screenTurnTarget) error {
	switch action {
	case screenTurnActionAsk:
		u.selectScreenTurnForAsk(target)
		return nil
	case screenTurnActionFork:
		if u.busy {
			return fmt.Errorf("fork cannot run while a turn is running")
		}
		if target.id == "" {
			return fmt.Errorf("selected turn has no fork handle")
		}
		if u.client == nil {
			return fmt.Errorf("fork requires app-server")
		}
		thread, err := u.client.ForkThreadAt(u.thread.ID, target.id, u.overrides)
		if err != nil {
			return err
		}
		u.adoptCommandThread(thread)
		u.commandMessage(fmt.Sprintf("forked from turn %d · %s", target.ordinal, shortID(thread.ID)), green)
	}
	return nil
}

func (u *ui) commandCompact() error {
	if u.busy {
		return fmt.Errorf("compact cannot run while a turn is running")
	}
	if u.client == nil {
		return fmt.Errorf("compact requires app-server")
	}
	if err := u.client.CompactThread(u.thread.ID); err != nil {
		return err
	}
	u.commandMessage("compaction started", cyan)
	return nil
}

func (u *ui) commandGoal(args string) error {
	if u.client == nil {
		u.commandMessage("goal requires app-server", yellow)
		return nil
	}
	args = strings.TrimSpace(args)
	if strings.EqualFold(args, "clear") {
		if err := u.client.ClearThreadGoal(u.thread.ID); err != nil {
			return err
		}
		u.commandMessage("goal cleared", green)
		return nil
	}
	if args == "" {
		goal, err := u.client.GetThreadGoal(u.thread.ID)
		if err != nil {
			return err
		}
		if goal == nil {
			u.commandMessage("no active goal", cyan)
			return nil
		}
		u.showCommandOutput("goal", []string{
			"objective · " + stringValue(goal["objective"]),
			"status · " + stringValue(goal["status"]),
		})
		return nil
	}
	goal, err := u.client.SetThreadGoal(u.thread.ID, args)
	if err != nil {
		return err
	}
	u.showCommandOutput("goal", []string{"objective · " + stringValue(goal["objective"])})
	return nil
}

func (u *ui) commandCopy() error {
	value := ""
	for index := len(u.transcript.entries) - 1; index >= 0; index-- {
		if u.transcript.entries[index].kind == screenAssistant {
			value = u.transcript.entries[index].text
			break
		}
	}
	if value == "" {
		for turnIndex := len(u.thread.Turns) - 1; turnIndex >= 0 && value == ""; turnIndex-- {
			for itemIndex := len(u.thread.Turns[turnIndex].Items) - 1; itemIndex >= 0; itemIndex-- {
				if u.thread.Turns[turnIndex].Items[itemIndex].Type == "agentMessage" {
					value = u.thread.Turns[turnIndex].Items[itemIndex].Text
					break
				}
			}
		}
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("no assistant response to copy")
	}
	writer := u.clipboard
	if writer == nil {
		writer = writeClipboard
	}
	if err := writer(value); err != nil {
		return fmt.Errorf("copy response: %w", err)
	}
	u.commandMessage(fmt.Sprintf("copied %d chars", len([]rune(value))), green)
	return nil
}

func (u *ui) commandDiff() error {
	command := exec.Command("git", "-C", u.cwd, "diff", "--no-ext-diff", "--no-color", "--")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git diff: %w", err)
	}
	u.showCommandOutput("git diff", []string{limitCommandOutput(string(output))})
	return nil
}

func (u *ui) commandStop() error {
	if !u.busy {
		u.commandMessage("no active turn", cyan)
		return nil
	}
	if u.turnID == "" || u.client == nil {
		u.commandMessage("active turn has no interrupt handle", yellow)
		return nil
	}
	if err := u.client.Interrupt(u.thread.ID, u.turnID); err != nil {
		return err
	}
	u.commandMessage("interrupt requested", yellow)
	return nil
}

func (u *ui) commandMCP() error {
	if u.client == nil {
		u.commandMessage("mcp requires app-server", yellow)
		return nil
	}
	servers, err := u.client.ListMCPServers(u.thread.ID)
	if err != nil {
		return err
	}
	lines := make([]string, 0, len(servers))
	for _, server := range servers {
		line := fmt.Sprintf("%s · %d tools", server.Name, len(server.Tools))
		if server.ToolsError != nil && *server.ToolsError != "" {
			line += " · error"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, "no MCP servers returned")
	}
	u.showCommandOutput("MCP servers", lines)
	return nil
}

func (u *ui) commandApps() error {
	if u.client == nil {
		u.commandMessage("apps requires app-server", yellow)
		return nil
	}
	apps, err := u.client.ListApps()
	if err != nil {
		return err
	}
	lines := make([]string, 0, len(apps))
	for _, app := range apps {
		state := "disabled"
		if app.Enabled {
			state = "enabled"
		}
		lines = append(lines, fmt.Sprintf("%s · %s", valueOr(app.Name, app.ID), state))
	}
	if len(lines) == 0 {
		lines = append(lines, "no apps returned")
	}
	u.showCommandOutput("apps", lines)
	return nil
}

func (u *ui) commandPlugins() error {
	if u.client == nil {
		u.commandMessage("plugins requires app-server", yellow)
		return nil
	}
	marketplaces, err := u.client.ListPlugins(u.cwd)
	if err != nil {
		return err
	}
	lines := make([]string, 0)
	for _, marketplace := range marketplaces {
		for _, plugin := range marketplace.Plugins {
			state := "available"
			if plugin.Installed {
				state = "installed"
			}
			if plugin.Installed && !plugin.Enabled {
				state = "disabled"
			}
			lines = append(lines, fmt.Sprintf("%s · %s", plugin.Name, state))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "no plugins returned")
	}
	u.showCommandOutput("plugins", lines)
	return nil
}

func (u *ui) commandDebugConfig() error {
	if u.client == nil {
		u.commandMessage("debug-config requires app-server", yellow)
		return nil
	}
	var result struct {
		Origins map[string]json.RawMessage `json:"origins"`
		Layers  []json.RawMessage          `json:"layers"`
	}
	if err := u.client.Call("config/read", map[string]any{
		"cwd":           u.cwd,
		"includeLayers": true,
	}, &result); err != nil {
		return err
	}
	u.showCommandOutput("debug config", []string{
		fmt.Sprintf("cwd · %s", u.cwd),
		fmt.Sprintf("origins · %d", len(result.Origins)),
		fmt.Sprintf("layers · %d", len(result.Layers)),
	})
	return nil
}

func (u *ui) beginCommandTurn(turnID string) {
	u.showWelcome = false
	u.busy = true
	u.turnID = turnID
	u.assistant = false
	u.tool = false
	u.toolOutput = false
	u.reasoningActive = false
	u.reasoningShown = false
	u.reasoningFrame = 0
	u.recap = turnRecap{}
	if u.screenMode {
		u.beginScreenTurn(turnID)
	}
}

func (u *ui) adoptCommandThread(thread threadSummary) {
	u.endScreenTurn()
	u.clearReplyTarget()
	u.thread = thread
	u.cwd = valueOr(thread.CWD, u.cwd)
	u.branch = gitBranch(u.cwd)
	u.clearScreenTranscript()
	u.scrollOffset = 0
	u.hoverText = ""
	u.usage = threadUsage{}
	u.recap = turnRecap{}
	u.turnID = ""
	u.busy = false
	u.assistant = false
	u.tool = false
	u.toolOutput = false
	u.cancelAndSend = ""
	u.dashboard = false
	u.dashboardError = ""
	u.showWelcome = len(thread.Turns) == 0
	u.renderHistory()
}

func (u *ui) commandMessage(message, color string) {
	u.printStatus(message, color)
	if u.screenMode {
		return
	}
	if u.busy {
		u.renderBusy()
		return
	}
	u.renderPrompt()
}

func (u *ui) showCommandOutput(title string, lines []string) {
	if len(lines) == 0 {
		lines = []string{"no output"}
	}
	if u.screenMode {
		u.showWelcome = false
		u.screenAdd(screenInfo, title+"\n"+strings.Join(lines, "\n"))
		return
	}
	u.clearPrompt()
	fmt.Fprintf(u.out, "\n%s%s%s\n", bold, sanitizeText(title), reset)
	for _, line := range lines {
		fmt.Fprintf(u.out, "  %s\n", sanitizeText(line))
	}
	u.renderPrompt()
}

func (u *ui) nativeCommandUnavailable(name string) {
	u.commandMessage("/"+name+" is available in the Codex catalog; this Lumen build does not expose its action yet", yellow)
}

func limitCommandOutput(value string) string {
	const maxRunes = 12000
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "\n… output truncated"
}

func stringValue(value any) string {
	if value == nil {
		return "none"
	}
	if text, ok := value.(string); ok && text != "" {
		return text
	}
	return fmt.Sprint(value)
}
