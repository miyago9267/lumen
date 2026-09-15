package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const cursor = '▌'

var (
	reset  = "\x1b[0m"
	muted  = "\x1b[38;5;245m"
	cyan   = "\x1b[38;5;81m"
	green  = "\x1b[38;5;114m"
	yellow = "\x1b[38;5;221m"
	red    = "\x1b[38;5;203m"
	bold   = "\x1b[1m"
)

func init() {
	if os.Getenv("NO_COLOR") != "" {
		reset, muted, cyan, green, yellow, red, bold = "", "", "", "", "", "", ""
	}
}

var errQuit = errors.New("quit")

type keyType int

const (
	keyRune keyType = iota
	keyEnter
	keyCtrlEnter
	keyAltEnter
	keyBackspace
	keyLeft
	keyRight
	keyUp
	keyDown
	keyHome
	keyEnd
	keyPageUp
	keyPageDown
	keyScrollUp
	keyScrollDown
	keyMouseClick
	keyMouseMove
	keyCtrlC
	keyCtrlX
	keyCtrlO
	keyCtrlBackslash
	keyCtrlD
	keyEscape
)

type keyEvent struct {
	typ            keyType
	rune           rune
	mouseX, mouseY int
}

func readKeys(input io.Reader, keys chan<- keyEvent) {
	reader := bufio.NewReader(input)
	defer close(keys)

	for {
		value, _, err := reader.ReadRune()
		if err != nil {
			return
		}
		switch value {
		case 3:
			keys <- keyEvent{typ: keyCtrlC}
		case 24:
			keys <- keyEvent{typ: keyCtrlX}
		case 15:
			keys <- keyEvent{typ: keyCtrlO}
		case 28:
			keys <- keyEvent{typ: keyCtrlBackslash}
		case 4:
			keys <- keyEvent{typ: keyCtrlD}
		case '\r', '\n':
			keys <- keyEvent{typ: keyEnter}
		case 8, 127:
			keys <- keyEvent{typ: keyBackspace}
		case 27:
			readEscape(reader, input, keys)
		default:
			keys <- keyEvent{typ: keyRune, rune: value}
		}
	}
}

func readEscape(reader *bufio.Reader, input io.Reader, keys chan<- keyEvent) {
	value, _, err := readEscapeRune(reader, input)
	if err != nil {
		keys <- keyEvent{typ: keyEscape}
		return
	}
	if value == '\r' || value == '\n' {
		keys <- keyEvent{typ: keyAltEnter}
		return
	}
	if value != '[' {
		keys <- keyEvent{typ: keyEscape}
		return
	}
	var sequence strings.Builder
	for {
		value, _, readErr := reader.ReadRune()
		if readErr != nil {
			return
		}
		sequence.WriteRune(value)
		if (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z') || value == '~' {
			break
		}
	}
	sequenceValue := sequence.String()
	if strings.HasPrefix(sequenceValue, "<") {
		if mouse, ok := parseSGRMouse(sequenceValue); ok {
			keys <- mouse
		}
		return
	}
	if modified, ok := parseModifiedKey(sequenceValue); ok {
		keys <- modified
		return
	}
	switch sequenceValue {
	case "A":
		keys <- keyEvent{typ: keyUp}
	case "B":
		keys <- keyEvent{typ: keyDown}
	case "C":
		keys <- keyEvent{typ: keyRight}
	case "D":
		keys <- keyEvent{typ: keyLeft}
	case "H", "1~", "7~":
		keys <- keyEvent{typ: keyHome}
	case "F", "4~", "8~":
		keys <- keyEvent{typ: keyEnd}
	case "5~":
		keys <- keyEvent{typ: keyPageUp}
	case "6~":
		keys <- keyEvent{typ: keyPageDown}
	default:
		keys <- keyEvent{typ: keyEscape}
	}
}

func readEscapeRune(reader *bufio.Reader, input io.Reader) (rune, int, error) {
	file, ok := input.(*os.File)
	if !ok || reader.Buffered() > 0 {
		return reader.ReadRune()
	}
	pollFD := []unix.PollFd{{Fd: int32(file.Fd()), Events: unix.POLLIN}}
	if _, err := unix.Poll(pollFD, 50); err != nil {
		return 0, 0, err
	}
	if pollFD[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) == 0 {
		return 0, 0, io.EOF
	}
	return reader.ReadRune()
}

func parseModifiedKey(sequence string) (keyEvent, bool) {
	if strings.HasSuffix(sequence, "u") {
		fields := strings.Split(strings.TrimSuffix(sequence, "u"), ";")
		if len(fields) == 2 && fields[0] == "13" {
			modifier, err := strconv.Atoi(fields[1])
			if err == nil && modifier&4 != 0 {
				return keyEvent{typ: keyCtrlEnter}, true
			}
		}
	}
	if strings.HasSuffix(sequence, "~") {
		fields := strings.Split(strings.TrimSuffix(sequence, "~"), ";")
		if len(fields) == 2 && fields[1] == "13" {
			modifier, err := strconv.Atoi(fields[0])
			if err == nil && modifier&4 != 0 {
				return keyEvent{typ: keyCtrlEnter}, true
			}
		}
		if len(fields) == 3 && fields[0] == "27" && fields[2] == "13" {
			modifier, err := strconv.Atoi(fields[1])
			if err == nil && modifier&4 != 0 {
				return keyEvent{typ: keyCtrlEnter}, true
			}
		}
	}
	return keyEvent{}, false
}

func parseSGRMouse(sequence string) (keyEvent, bool) {
	if len(sequence) < 6 || sequence[0] != '<' || sequence[len(sequence)-1] != 'M' {
		return keyEvent{}, false
	}
	fields := strings.Split(sequence[1:len(sequence)-1], ";")
	if len(fields) != 3 {
		return keyEvent{}, false
	}
	button, err := strconv.Atoi(fields[0])
	if err != nil {
		return keyEvent{}, false
	}
	x, err := strconv.Atoi(fields[1])
	if err != nil {
		return keyEvent{}, false
	}
	y, err := strconv.Atoi(fields[2])
	if err != nil {
		return keyEvent{}, false
	}
	if button&64 != 0 {
		if button&3 == 0 {
			return keyEvent{typ: keyScrollUp, mouseX: x, mouseY: y}, true
		}
		if button&3 == 1 {
			return keyEvent{typ: keyScrollDown, mouseX: x, mouseY: y}, true
		}
		return keyEvent{}, false
	}
	if button&32 != 0 {
		return keyEvent{typ: keyMouseMove, mouseX: x, mouseY: y}, true
	}
	if button&3 == 0 {
		return keyEvent{typ: keyMouseClick, mouseX: x, mouseY: y}, true
	}
	return keyEvent{}, false
}

type approvalRequest struct {
	id        json.RawMessage
	available []string
	action    string
	cwd       string
	reason    string
}

type userInputOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type userInputQuestion struct {
	Header   string            `json:"header"`
	ID       string            `json:"id"`
	Question string            `json:"question"`
	Options  []userInputOption `json:"options"`
	IsOther  bool              `json:"isOther"`
	IsSecret bool              `json:"isSecret"`
}

type userInputAnswer struct {
	Answers []string `json:"answers"`
}

type userInputRequest struct {
	id        json.RawMessage
	questions []userInputQuestion
	answers   map[string]userInputAnswer
	index     int
}

type threadUsage struct {
	totalTokens        int64
	modelContextWindow int64
	hasContextWindow   bool
}

type turnRecap struct {
	commands    []string
	toolCount   int
	outputs     int
	fileChanges int
}

type ui struct {
	client     *appServer
	thread     threadSummary
	cwd        string
	fullscreen bool
	screenMode bool
	out        io.Writer
	in         io.Reader
	overrides  runtimeOverrides

	input          []rune
	cursor         int
	promptLines    int
	busy           bool
	turnID         string
	assistant      bool
	tool           bool
	toolOutput     bool
	shortcuts      bool
	showToolOutput bool
	dashboard      bool
	dashboardRows  []threadSummary
	dashboardIndex int
	dashboardError string
	approval       *approvalRequest
	inputRequest   *userInputRequest
	inputHistory   []string
	historyIndex   int
	savedInput     []rune
	queuedPrompts  []string
	cancelAndSend  string
	lastOutput     time.Time
	branch         string
	screenBlocks   []screenBlock
	usage          threadUsage
	scrollOffset   int
	recap          turnRecap
	hoverText      string
}

func newUI(client *appServer, thread threadSummary, cwd string, fullscreen bool, overrides runtimeOverrides) *ui {
	return &ui{
		client:       client,
		thread:       thread,
		cwd:          cwd,
		fullscreen:   fullscreen,
		overrides:    overrides,
		historyIndex: -1,
		out:          os.Stdout,
		in:           os.Stdin,
	}
}

func (u *ui) run(initialPrompt string) error {
	interactive := term.IsTerminal(int(os.Stdin.Fd()))
	if u.fullscreen && interactive {
		return u.runFullscreen(initialPrompt)
	}
	return u.runInline(initialPrompt, interactive)
}

func (u *ui) runInline(initialPrompt string, interactive bool) error {
	if interactive {
		state, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("enable raw terminal: %w", err)
		}
		defer term.Restore(int(os.Stdin.Fd()), state)
	}

	fmt.Fprint(u.out, "\x1b[?25l")
	defer fmt.Fprint(u.out, "\x1b[?25h\n")

	u.renderHeader()
	u.renderHistory()
	var keys <-chan keyEvent
	if interactive {
		keyEvents := make(chan keyEvent, 8)
		go readKeys(u.in, keyEvents)
		keys = keyEvents
	}

	if initialPrompt != "" {
		if err := u.submit(initialPrompt); err != nil {
			return err
		}
	} else {
		u.renderPrompt()
	}

	for {
		select {
		case key, ok := <-keys:
			if !ok {
				return nil
			}
			if err := u.handleKey(key); errors.Is(err, errQuit) {
				return nil
			} else if err != nil {
				u.printError(err)
				if !u.busy && u.approval == nil {
					u.renderPrompt()
				}
			}
		case request := <-u.client.requests:
			if !interactive {
				_ = u.client.respondError(request.id, -32001, "lumen requires an interactive terminal for approval")
				return errors.New("approval requires an interactive terminal")
			}
			u.handleServerRequest(request)
		case notification := <-u.client.notifications:
			u.handleNotification(notification)
			if !interactive && !u.busy {
				return nil
			}
		case err := <-u.client.errors:
			if err != nil {
				u.printError(err)
				return err
			}
		case <-u.client.closed:
			return errors.New("app-server closed")
		}
	}
}

func (u *ui) renderHeader() {
	if u.screenMode {
		return
	}
	model := u.effectiveModel()
	effort := u.effectiveEffort()
	approval := u.effectiveApproval()
	sandbox := u.effectiveSandbox()
	fmt.Fprintf(u.out, "%s%sLumen%s  %s%s%s  %s%s%s\n", bold, cyan, reset, muted, model, reset, muted, effort, reset)
	fmt.Fprintf(u.out, "%s%s%s  %sthread %s%s · %s%s%s · %ssandbox %s%s", bold, displayCWD(u.cwd), reset, muted, shortID(u.thread.ID), reset, muted, approval, reset, muted, sandbox, reset)
	if name := strings.TrimSpace(u.thread.Name); name != "" {
		fmt.Fprintf(u.out, " · %sname %s%s", muted, sanitizeText(name), reset)
	}
	fmt.Fprintln(u.out)
	fmt.Fprintf(u.out, "%sEnter%s send · %sAlt+Enter%s newline · %sCtrl-C%s interrupt/quit · %s/help%s\n\n", cyan, reset, cyan, reset, cyan, reset, cyan, reset)
}

func (u *ui) effectiveModel() string {
	return valueOr(valueOr(u.overrides.model, u.thread.Model), "configured model")
}

func (u *ui) effectiveEffort() string {
	return valueOr(valueOr(u.overrides.reasoningEffort, u.thread.ReasoningEffort), "configured effort")
}

func (u *ui) effectiveApproval() string {
	return valueOr(valueOr(u.overrides.approvalPolicy, u.thread.ApprovalPolicy), "configured approval")
}

func (u *ui) effectiveSandbox() string {
	if u.overrides.sandbox != "" {
		return u.overrides.sandbox
	}
	return sandboxLabel(u.thread.Sandbox)
}

func (u *ui) renderHistory() {
	if len(u.thread.Turns) == 0 {
		return
	}
	start := 0
	if len(u.thread.Turns) > 20 {
		start = len(u.thread.Turns) - 20
	}
	if u.screenMode {
		if start > 0 {
			u.screenAdd(screenEvent, fmt.Sprintf("recent history · %d earlier turns hidden", start))
		}
		for _, turn := range u.thread.Turns[start:] {
			for _, item := range turn.Items {
				u.renderHistoryItem(item)
			}
		}
		return
	}
	fmt.Fprintf(u.out, "%srecent history%s\n", muted, reset)
	if start > 0 {
		fmt.Fprintf(u.out, "%s  … %d earlier turns hidden%s\n", muted, start, reset)
	}
	for _, turn := range u.thread.Turns[start:] {
		for _, item := range turn.Items {
			u.renderHistoryItem(item)
		}
	}
	fmt.Fprintln(u.out)
}

func (u *ui) renderHistoryItem(item historyItem) {
	if u.screenMode {
		u.renderScreenHistoryItem(item)
		return
	}
	switch item.Type {
	case "userMessage":
		if text := historyText(item.Content); text != "" {
			fmt.Fprintf(u.out, "\n%s› you%s\n%s\n", bold, reset, sanitizeText(text))
		}
	case "agentMessage":
		if item.Text != "" {
			fmt.Fprintf(u.out, "\n%s› assistant%s %s\n", green, reset, sanitizeText(item.Text))
		}
	case "commandExecution":
		if item.Command != "" {
			fmt.Fprintf(u.out, "\n%s  $ %s%s\n", yellow, sanitizeText(item.Command), reset)
		}
		if item.AggregatedOutput != "" {
			fmt.Fprint(u.out, indentText(sanitizeText(item.AggregatedOutput), "    "))
			fmt.Fprintln(u.out)
		}
		if item.Status != "" {
			status := sanitizeText(item.Status)
			if exitCode := numberString(item.ExitCode); exitCode != "" {
				status = fmt.Sprintf("%s exit %s", status, exitCode)
			}
			fmt.Fprintf(u.out, "%s  %s%s\n", statusColor(status), status, reset)
		}
	case "fileChange":
		if item.Status != "" {
			fmt.Fprintf(u.out, "%s  file change %s%s\n", statusColor(item.Status), sanitizeText(item.Status), reset)
		}
	case "mcpToolCall":
		fmt.Fprintf(u.out, "%s  ◇ %s/%s%s\n", yellow, sanitizeText(item.Server), sanitizeText(item.Tool), reset)
	case "plan":
		if item.Text != "" {
			fmt.Fprintf(u.out, "%s  plan%s\n%s\n", cyan, reset, indentText(sanitizeText(item.Text), "    "))
		}
	case "reasoning":
		if len(item.Summary) > 0 {
			fmt.Fprintf(u.out, "%s  … reasoning: %s%s\n", muted, sanitizeText(strings.Join(item.Summary, " ")), reset)
		}
	}
}

func historyText(content []historyInput) string {
	var parts []string
	for _, item := range content {
		if item.Type == "text" && item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (u *ui) handleKey(key keyEvent) error {
	if u.screenMode && key.typ == keyCtrlBackslash {
		if u.approval != nil || u.inputRequest != nil {
			return nil
		}
		return u.toggleDashboard()
	}
	if u.screenMode && u.dashboard && u.approval == nil && u.inputRequest == nil {
		return u.handleDashboardKey(key)
	}
	if u.approval != nil {
		return u.handleApprovalKey(key)
	}
	if u.inputRequest != nil {
		return u.handleInputRequestKey(key)
	}
	if u.screenMode && key.typ == keyCtrlX {
		u.shortcuts = !u.shortcuts
		return nil
	}
	if u.screenMode && u.shortcuts {
		if key.typ == keyEscape || key.typ == keyCtrlC {
			u.shortcuts = false
		}
		return nil
	}
	if u.screenMode {
		if key.typ == keyCtrlO {
			u.showToolOutput = !u.showToolOutput
			return nil
		}
		switch key.typ {
		case keyMouseMove:
			width, height := terminalSize()
			u.updateHover(key.mouseX, key.mouseY, width, height)
			return nil
		case keyMouseClick:
			width, height := terminalSize()
			u.updateHover(key.mouseX, key.mouseY, width, height)
			u.placeCursorFromMouse(key.mouseX, key.mouseY, width, height)
			return nil
		case keyPageUp:
			u.scrollOffset += 8
			return nil
		case keyPageDown:
			u.scrollOffset -= 8
			if u.scrollOffset < 0 {
				u.scrollOffset = 0
			}
			return nil
		case keyScrollUp:
			u.scrollOffset += 3
			return nil
		case keyScrollDown:
			u.scrollOffset -= 3
			if u.scrollOffset < 0 {
				u.scrollOffset = 0
			}
			return nil
		}
	}
	if u.busy {
		if key.typ == keyCtrlC {
			if u.turnID == "" {
				return nil
			}
			u.printStatus("interrupt requested", yellow)
			return u.client.Interrupt(u.thread.ID, u.turnID)
		}
		if key.typ == keyCtrlEnter {
			return u.cancelAndSendPrompt()
		}
		if key.typ == keyEnter {
			return u.queuePrompt()
		}
		if u.editComposer(key) {
			return nil
		}
		return nil
	}

	switch key.typ {
	case keyCtrlC, keyCtrlD, keyEscape:
		return errQuit
	case keyEnter, keyCtrlEnter:
		value := strings.TrimSpace(string(u.input))
		if value != "" {
			if strings.HasPrefix(value, "/") {
				return u.handleCommand(value)
			}
			return u.submit(value)
		}
	case keyAltEnter:
		u.insertRune('\n')
		u.renderPrompt()
	default:
		u.editComposer(key)
	}
	return nil
}

func (u *ui) editComposer(key keyEvent) bool {
	switch key.typ {
	case keyRune:
		u.insertRune(key.rune)
	case keyBackspace:
		if u.cursor == 0 {
			return true
		}
		u.markInputEdited()
		u.input = append(u.input[:u.cursor-1], u.input[u.cursor:]...)
		u.cursor--
	case keyLeft:
		if u.cursor > 0 {
			u.cursor--
		}
	case keyRight:
		if u.cursor < len(u.input) {
			u.cursor++
		}
	case keyHome:
		u.cursor = lineStart(u.input, u.cursor)
	case keyEnd:
		u.cursor = lineEnd(u.input, u.cursor)
	case keyUp:
		u.movePromptUp()
	case keyDown:
		u.movePromptDown()
	case keyAltEnter:
		u.insertRune('\n')
	default:
		return false
	}
	u.renderPrompt()
	return true
}

func (u *ui) handleCommand(value string) error {
	command := strings.Fields(value)
	if len(command) == 0 {
		return nil
	}
	switch command[0] {
	case "/help":
		u.showHelp()
	case "/status":
		u.showStatus()
	case "/dashboard":
		u.input = nil
		u.cursor = 0
		return u.openDashboard()
	case "/quit", "/exit", "/q":
		return errQuit
	default:
		return fmt.Errorf("unknown command %q; try /help", command[0])
	}
	return nil
}

func (u *ui) showHelp() {
	u.clearPrompt()
	u.input = nil
	u.cursor = 0
	if u.screenMode {
		u.screenAdd(screenInfo, "commands\n  /help       show this help\n  /status     show thread and policy state\n  /dashboard  supervise top-level sessions\n  /quit       exit without changing the official Codex CLI")
		return
	}
	fmt.Fprintf(u.out, "\n%scommands%s\n", bold, reset)
	fmt.Fprintf(u.out, "  %s/help%s     show this help\n", cyan, reset)
	fmt.Fprintf(u.out, "  %s/status%s   show thread and policy state\n", cyan, reset)
	fmt.Fprintf(u.out, "  %s/quit%s     exit without changing the official Codex CLI\n", cyan, reset)
	fmt.Fprintln(u.out)
	u.renderPrompt()
}

func (u *ui) showStatus() {
	u.clearPrompt()
	u.input = nil
	u.cursor = 0
	if u.screenMode {
		u.screenAdd(screenInfo, fmt.Sprintf("status\n  model       %s\n  effort      %s\n  approval    %s\n  sandbox     %s\n  cwd         %s\n  thread      %s\n  state       %s", u.effectiveModel(), u.effectiveEffort(), u.effectiveApproval(), u.effectiveSandbox(), u.cwd, u.thread.ID, map[bool]string{true: "working", false: "idle"}[u.busy]))
		return
	}
	model := valueOr(u.overrides.model, u.thread.Model)
	effort := valueOr(u.overrides.reasoningEffort, u.thread.ReasoningEffort)
	approval := valueOr(u.overrides.approvalPolicy, u.thread.ApprovalPolicy)
	sandbox := sandboxLabel(u.thread.Sandbox)
	if u.overrides.sandbox != "" {
		sandbox = u.overrides.sandbox
	}
	fmt.Fprintf(u.out, "\n%sstatus%s\n", bold, reset)
	fmt.Fprintf(u.out, "  model       %s\n", sanitizeText(valueOr(model, "configured")))
	fmt.Fprintf(u.out, "  effort      %s\n", sanitizeText(valueOr(effort, "configured")))
	fmt.Fprintf(u.out, "  approval    %s\n", sanitizeText(valueOr(approval, "configured")))
	fmt.Fprintf(u.out, "  sandbox     %s\n", sanitizeText(sandbox))
	fmt.Fprintf(u.out, "  cwd         %s\n", sanitizeText(u.cwd))
	fmt.Fprintf(u.out, "  thread      %s\n", sanitizeText(u.thread.ID))
	fmt.Fprintf(u.out, "  state       %s\n\n", map[bool]string{true: "working", false: "idle"}[u.busy])
	u.renderPrompt()
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (u *ui) insertRune(value rune) {
	u.markInputEdited()
	u.input = append(u.input, 0)
	copy(u.input[u.cursor+1:], u.input[u.cursor:])
	u.input[u.cursor] = value
	u.cursor++
}

func lineStart(value []rune, cursor int) int {
	for cursor > 0 && value[cursor-1] != '\n' {
		cursor--
	}
	return cursor
}

func lineEnd(value []rune, cursor int) int {
	for cursor < len(value) && value[cursor] != '\n' {
		cursor++
	}
	return cursor
}

func (u *ui) moveVertical(direction int) {
	currentStart := lineStart(u.input, u.cursor)
	column := u.cursor - currentStart
	if direction < 0 {
		if currentStart == 0 {
			return
		}
		previousEnd := currentStart - 1
		previousStart := lineStart(u.input, previousEnd)
		u.cursor = min(previousStart+column, previousEnd)
		return
	}
	currentEnd := lineEnd(u.input, u.cursor)
	if currentEnd == len(u.input) {
		return
	}
	nextStart := currentEnd + 1
	nextEnd := lineEnd(u.input, nextStart)
	u.cursor = min(nextStart+column, nextEnd)
}

func (u *ui) submit(value string) error {
	u.clearPrompt()
	u.rememberPrompt(value)
	u.input = nil
	u.cursor = 0
	u.assistant = false
	u.tool = false
	u.toolOutput = false
	u.recap = turnRecap{}
	if u.screenMode {
		u.screenAdd(screenUser, value)
	} else {
		fmt.Fprintf(u.out, "%s› you%s\n%s%s%s\n", bold, reset, muted, sanitizeText(value), reset)
	}
	u.busy = true
	if !u.screenMode {
		fmt.Fprintf(u.out, "%s  … working%s%s\n", muted, reset, "")
	}
	turnID, err := u.client.StartTurn(u.thread.ID, value, u.overrides)
	if err != nil {
		u.busy = false
		u.renderPrompt()
		return err
	}
	u.turnID = turnID
	return nil
}

func (u *ui) handleNotification(message rpcMessage) {
	switch message.Method {
	case "turn/started":
		var params struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.ThreadID == u.thread.ID {
			u.turnID = params.Turn.ID
		}
	case "item/started":
		u.handleItemStarted(message.Params)
	case "item/agentMessage/delta":
		var params struct {
			ThreadID string `json:"threadId"`
			Delta    string `json:"delta"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.ThreadID == u.thread.ID {
			if u.screenMode {
				u.screenAppend(screenAssistant, params.Delta)
				u.assistant = true
				u.lastOutput = time.Now()
				return
			}
			if !u.assistant {
				u.clearPrompt()
				fmt.Fprintf(u.out, "\n%s› assistant%s ", green, reset)
				u.assistant = true
			}
			fmt.Fprint(u.out, sanitizeText(params.Delta))
			u.lastOutput = time.Now()
		}
	case "item/commandExecution/outputDelta":
		var params struct {
			ThreadID string `json:"threadId"`
			Delta    string `json:"delta"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.ThreadID == u.thread.ID {
			firstOutput := !u.toolOutput
			if firstOutput {
				u.recap.outputs++
				u.toolOutput = true
			}
			if u.screenMode {
				if firstOutput {
					u.screenAdd(screenTool, "output")
				}
				u.screenAppend(screenTool, params.Delta)
				u.tool = true
				u.lastOutput = time.Now()
				return
			}
			if !u.tool {
				u.clearPrompt()
				fmt.Fprintf(u.out, "\n%s  output%s\n", muted, reset)
				u.tool = true
			}
			fmt.Fprint(u.out, indentText(sanitizeText(params.Delta), "    "))
			u.lastOutput = time.Now()
		}
	case "item/completed":
		u.handleItemCompleted(message.Params)
	case "turn/plan/updated":
		var params struct {
			ThreadID string `json:"threadId"`
			Plan     []struct {
				Step   string `json:"step"`
				Status string `json:"status"`
			} `json:"plan"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.ThreadID == u.thread.ID {
			if u.screenMode {
				var planLines []string
				for _, item := range params.Plan {
					planLines = append(planLines, fmt.Sprintf("[%s] %s", item.Status, item.Step))
				}
				u.screenAdd(screenPlan, strings.Join(planLines, "\n"))
				return
			}
			u.clearPrompt()
			fmt.Fprintf(u.out, "\n%s  plan%s", cyan, reset)
			for _, item := range params.Plan {
				fmt.Fprintf(u.out, "\n    [%s] %s", sanitizeText(item.Status), sanitizeText(item.Step))
			}
			fmt.Fprintln(u.out)
		}
	case "thread/name/updated":
		var params struct {
			ThreadID string `json:"threadId"`
			Name     string `json:"name"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.ThreadID == u.thread.ID {
			u.thread.Name = params.Name
		}
	case "thread/tokenUsage/updated":
		var params struct {
			ThreadID   string `json:"threadId"`
			TokenUsage struct {
				Total struct {
					TotalTokens int64 `json:"totalTokens"`
				} `json:"total"`
				ModelContextWindow *int64 `json:"modelContextWindow"`
			} `json:"tokenUsage"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.ThreadID == u.thread.ID {
			u.usage.totalTokens = params.TokenUsage.Total.TotalTokens
			if params.TokenUsage.ModelContextWindow != nil {
				u.usage.modelContextWindow = *params.TokenUsage.ModelContextWindow
				u.usage.hasContextWindow = true
			}
		}
	case "turn/completed":
		var params struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				Status string `json:"status"`
			} `json:"turn"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.ThreadID == u.thread.ID {
			u.finishTurn(params.Turn.Status)
		}
	case "error":
		var params struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(message.Params, &params) == nil {
			u.printError(errors.New(params.Message))
		}
	case "warning", "configWarning", "guardianWarning":
		var params struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.Message != "" {
			u.printStatus(compactWarning(params.Message), yellow)
		}
	}
}

func compactWarning(message string) string {
	const prefix = "Under-development features enabled: "
	if !strings.HasPrefix(message, prefix) {
		return message
	}
	features := strings.TrimSpace(strings.SplitN(strings.TrimPrefix(message, prefix), ".", 2)[0])
	if features == "" {
		return "Codex warning: unstable features active"
	}
	return "Codex warning: unstable features active (" + features + ")"
}

func (u *ui) handleItemStarted(raw json.RawMessage) {
	var params struct {
		ThreadID string         `json:"threadId"`
		Item     map[string]any `json:"item"`
	}
	if json.Unmarshal(raw, &params) != nil || params.ThreadID != u.thread.ID {
		return
	}
	typeName, _ := params.Item["type"].(string)
	switch typeName {
	case "commandExecution":
		command, _ := params.Item["command"].(string)
		u.recap.toolCount++
		u.toolOutput = false
		if command != "" {
			u.recap.commands = append(u.recap.commands, command)
		}
		if u.screenMode {
			u.screenAdd(screenTool, "$ "+command)
			u.tool = true
			u.toolOutput = false
			return
		}
		u.clearPrompt()
		fmt.Fprintf(u.out, "\n%s  $ %s%s\n", yellow, sanitizeText(command), reset)
		u.tool = true
	case "fileChange":
		u.recap.toolCount++
		u.recap.fileChanges++
		u.toolOutput = false
		if u.screenMode {
			u.screenAdd(screenTool, "✎ file change")
			u.tool = true
			u.toolOutput = false
			return
		}
		u.clearPrompt()
		fmt.Fprintf(u.out, "\n%s  ✎ file change%s\n", yellow, reset)
		u.tool = true
	case "mcpToolCall":
		server, _ := params.Item["server"].(string)
		tool, _ := params.Item["tool"].(string)
		u.recap.toolCount++
		u.toolOutput = false
		if u.screenMode {
			u.screenAdd(screenTool, fmt.Sprintf("◇ %s/%s", server, tool))
			u.tool = true
			u.toolOutput = false
			return
		}
		u.clearPrompt()
		fmt.Fprintf(u.out, "\n%s  ◇ %s/%s%s\n", yellow, sanitizeText(server), sanitizeText(tool), reset)
		u.tool = true
	case "reasoning":
		if u.screenMode {
			u.screenAdd(screenEvent, "reasoning")
			return
		}
		u.clearPrompt()
		fmt.Fprintf(u.out, "\n%s  … reasoning%s\n", muted, reset)
	}
}

func (u *ui) handleItemCompleted(raw json.RawMessage) {
	var params struct {
		ThreadID string         `json:"threadId"`
		Item     map[string]any `json:"item"`
	}
	if json.Unmarshal(raw, &params) != nil || params.ThreadID != u.thread.ID {
		return
	}
	typeName, _ := params.Item["type"].(string)
	switch typeName {
	case "commandExecution":
		status, _ := params.Item["status"].(string)
		exitCode := numberString(params.Item["exitCode"])
		if exitCode != "" {
			status = fmt.Sprintf("%s exit %s", status, exitCode)
		}
		if u.screenMode {
			u.screenAdd(screenStatus, status)
			return
		}
		u.clearPrompt()
		fmt.Fprintf(u.out, "%s  %s%s\n", statusColor(status), sanitizeText(status), reset)
	case "fileChange":
		status, _ := params.Item["status"].(string)
		if u.screenMode {
			u.screenAdd(screenStatus, status)
			return
		}
		u.clearPrompt()
		fmt.Fprintf(u.out, "%s  %s%s\n", statusColor(status), sanitizeText(status), reset)
	}
}

func (u *ui) finishTurn(status string) {
	u.clearPrompt()
	if !u.screenMode && (u.assistant || u.tool) {
		fmt.Fprintln(u.out)
	}
	u.assistant = false
	u.tool = false
	u.toolOutput = false
	u.busy = false
	u.turnID = ""
	if status == "" {
		status = "completed"
	}
	if u.screenMode {
		u.screenAdd(screenInfo, u.recapText(status))
		u.screenAdd(screenStatus, "turn "+status)
		u.startNextPrompt()
		return
	}
	u.clearPrompt()
	fmt.Fprintf(u.out, "\n%s%s%s\n", cyan, u.recapText(status), reset)
	u.printStatus("turn "+status, statusColor(status))
	u.startNextPrompt()
	if u.busy {
		return
	}
	u.renderPrompt()
}

func (u *ui) handleServerRequest(request serverRequest) {
	switch request.method {
	case "item/commandExecution/requestApproval":
		var params struct {
			ThreadID           string   `json:"threadId"`
			TurnID             string   `json:"turnId"`
			ItemID             string   `json:"itemId"`
			Command            string   `json:"command"`
			CWD                string   `json:"cwd"`
			Reason             string   `json:"reason"`
			AvailableDecisions []string `json:"availableDecisions"`
		}
		if err := json.Unmarshal(request.params, &params); err != nil {
			u.rejectServerRequest(request, "invalid approval parameters")
			return
		}
		if err := u.validateServerRequest(params.ThreadID, params.TurnID); err != nil {
			u.rejectServerRequest(request, err.Error())
			return
		}
		if params.CWD == "" {
			params.CWD = u.cwd
		}
		u.showApproval(request, params.Command, params.CWD, params.Reason, params.AvailableDecisions)
	case "item/fileChange/requestApproval":
		var params struct {
			ThreadID           string   `json:"threadId"`
			TurnID             string   `json:"turnId"`
			Reason             string   `json:"reason"`
			AvailableDecisions []string `json:"availableDecisions"`
		}
		if err := json.Unmarshal(request.params, &params); err != nil {
			u.rejectServerRequest(request, "invalid file-change approval parameters")
			return
		}
		if err := u.validateServerRequest(params.ThreadID, params.TurnID); err != nil {
			u.rejectServerRequest(request, err.Error())
			return
		}
		u.showApproval(request, "apply file changes", u.cwd, params.Reason, params.AvailableDecisions)
	case "item/permissions/requestApproval":
		var params struct {
			ThreadID string          `json:"threadId"`
			TurnID   string          `json:"turnId"`
			Request  json.RawMessage `json:"permissions"`
		}
		if err := json.Unmarshal(request.params, &params); err != nil {
			u.rejectServerRequest(request, "invalid permission parameters")
			return
		}
		if err := u.validateServerRequest(params.ThreadID, params.TurnID); err != nil {
			u.rejectServerRequest(request, err.Error())
			return
		}
		if err := u.client.respond(request.id, map[string]any{
			"permissions": map[string]any{},
			"scope":       "turn",
		}); err != nil {
			u.printError(err)
			return
		}
		u.printStatus("permission request denied · no additional access granted", yellow)
	case "item/tool/requestUserInput":
		var params struct {
			ThreadID  string              `json:"threadId"`
			TurnID    string              `json:"turnId"`
			Questions []userInputQuestion `json:"questions"`
		}
		if err := json.Unmarshal(request.params, &params); err != nil {
			u.rejectServerRequest(request, "invalid user-input parameters")
			return
		}
		if err := u.validateServerRequest(params.ThreadID, params.TurnID); err != nil {
			u.rejectServerRequest(request, err.Error())
			return
		}
		for _, question := range params.Questions {
			if question.ID == "" || question.Question == "" {
				u.rejectServerRequest(request, "user-input question is missing id or question")
				return
			}
		}
		if len(params.Questions) == 0 {
			_ = u.client.respond(request.id, map[string]any{"answers": map[string]userInputAnswer{}})
			u.printStatus("user input request had no questions", yellow)
			return
		}
		u.clearPrompt()
		u.input = nil
		u.cursor = 0
		u.dashboard = false
		u.dashboardError = ""
		u.inputRequest = &userInputRequest{
			id:        request.id,
			questions: params.Questions,
			answers:   make(map[string]userInputAnswer, len(params.Questions)),
		}
		u.printStatus(fmt.Sprintf("input requested · %d question%s", len(params.Questions), pluralSuffix(len(params.Questions))), cyan)
		u.renderPrompt()
	default:
		u.rejectServerRequest(request, "unsupported server request: "+request.method)
	}
}

func (u *ui) validateServerRequest(threadID, turnID string) error {
	if threadID == "" {
		return errors.New("server request missing thread id")
	}
	if threadID != u.thread.ID {
		return errors.New("server request thread mismatch")
	}
	if turnID != "" && u.turnID != "" && turnID != u.turnID {
		return errors.New("server request turn mismatch")
	}
	return nil
}

func (u *ui) rejectServerRequest(request serverRequest, message string) {
	if u.screenMode {
		u.screenAdd(screenError, "request rejected · "+message)
	} else {
		u.clearPrompt()
		fmt.Fprintf(u.out, "\n%s  request rejected · %s%s\n", red, sanitizeText(message), reset)
	}
	if u.client != nil {
		_ = u.client.respondError(request.id, -32602, "lumen: "+message)
	}
}

func (u *ui) showApproval(request serverRequest, action, cwd, reason string, available []string) {
	u.clearPrompt()
	u.dashboard = false
	u.dashboardError = ""
	u.approval = &approvalRequest{id: request.id, available: available, action: action, cwd: cwd, reason: reason}
	if u.screenMode {
		return
	}
	fmt.Fprintf(u.out, "\n%s%s  approval required%s\n", bold, yellow, reset)
	fmt.Fprintf(u.out, "%s  action%s  %s\n", muted, reset, sanitizeText(action))
	fmt.Fprintf(u.out, "%s  cwd%s     %s\n", muted, reset, sanitizeText(cwd))
	if reason != "" {
		fmt.Fprintf(u.out, "%s  reason%s   %s\n", muted, reset, sanitizeText(reason))
	}
	fmt.Fprintf(u.out, "%s  %s%s\n", cyan, approvalHint(available), reset)
}

func (u *ui) handleApprovalKey(key keyEvent) error {
	decision := ""
	switch key.typ {
	case keyCtrlC, keyEscape:
		decision = "cancel"
	case keyEnter:
		decision = "accept"
	case keyRune:
		switch key.rune {
		case 'a', 'A':
			decision = "accept"
		case 's', 'S':
			decision = "acceptForSession"
		case 'd', 'D':
			decision = "decline"
		case 'c', 'C':
			decision = "cancel"
		}
	}
	if decision == "" {
		return nil
	}
	approval := u.approval
	if !decisionAllowed(approval.available, decision) {
		return nil
	}
	u.approval = nil
	if err := u.client.respond(approval.id, map[string]string{"decision": decision}); err != nil {
		return err
	}
	if u.screenMode {
		u.screenAdd(screenStatus, decision)
		return nil
	}
	fmt.Fprintf(u.out, "%s  %s%s\n", statusColor(decision), decision, reset)
	u.renderBusy()
	return nil
}

func (u *ui) handleInputRequestKey(key keyEvent) error {
	switch key.typ {
	case keyCtrlC:
		return u.finishInputRequest(true, true)
	case keyEscape:
		return u.finishInputRequest(true, false)
	case keyEnter:
		return u.submitInputAnswer()
	case keyRune:
		u.insertRune(key.rune)
		u.renderPrompt()
	case keyBackspace:
		if u.cursor > 0 {
			u.input = append(u.input[:u.cursor-1], u.input[u.cursor:]...)
			u.cursor--
			u.renderPrompt()
		}
	case keyLeft:
		if u.cursor > 0 {
			u.cursor--
			u.renderPrompt()
		}
	case keyRight:
		if u.cursor < len(u.input) {
			u.cursor++
			u.renderPrompt()
		}
	case keyHome:
		u.cursor = lineStart(u.input, u.cursor)
		u.renderPrompt()
	case keyEnd:
		u.cursor = lineEnd(u.input, u.cursor)
		u.renderPrompt()
	case keyUp:
		u.moveVertical(-1)
		u.renderPrompt()
	case keyDown:
		u.moveVertical(1)
		u.renderPrompt()
	case keyMouseClick:
		width, height := terminalSize()
		if u.placeCursorFromMouse(key.mouseX, key.mouseY, width, height) {
			u.renderPrompt()
		}
	}
	return nil
}

func (u *ui) submitInputAnswer() error {
	request := u.inputRequest
	if request == nil || request.index >= len(request.questions) {
		return nil
	}
	question := request.questions[request.index]
	request.answers[question.ID] = userInputAnswer{Answers: []string{string(u.input)}}
	request.index++
	u.input = nil
	u.cursor = 0
	if request.index < len(request.questions) {
		u.printStatus(fmt.Sprintf("input %d/%d", request.index+1, len(request.questions)), cyan)
		u.renderPrompt()
		return nil
	}
	return u.finishInputRequest(false, false)
}

func (u *ui) finishInputRequest(cancel, interrupt bool) error {
	request := u.inputRequest
	if request == nil {
		return nil
	}
	if cancel {
		for _, question := range request.questions {
			if _, ok := request.answers[question.ID]; !ok {
				request.answers[question.ID] = userInputAnswer{}
			}
		}
	}
	u.inputRequest = nil
	u.input = nil
	u.cursor = 0
	if err := u.client.respond(request.id, map[string]any{"answers": request.answers}); err != nil {
		return err
	}
	if cancel {
		u.printStatus("input canceled", yellow)
		if interrupt && u.turnID != "" {
			return u.client.Interrupt(u.thread.ID, u.turnID)
		}
		return nil
	}
	u.printStatus("input submitted", green)
	return nil
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func (u *ui) recapText(status string) string {
	lines := []string{"recap · " + valueOr(status, "completed")}
	toolLabel := "tool"
	if u.recap.toolCount != 1 {
		toolLabel += "s"
	}
	lines = append(lines, fmt.Sprintf("  %d %s", u.recap.toolCount, toolLabel))
	if u.recap.fileChanges > 0 {
		changeLabel := "file change"
		if u.recap.fileChanges != 1 {
			changeLabel += "s"
		}
		lines = append(lines, fmt.Sprintf("  %d %s", u.recap.fileChanges, changeLabel))
	}
	for index, command := range u.recap.commands {
		if index == 3 {
			lines = append(lines, fmt.Sprintf("  +%d more command%s", len(u.recap.commands)-index, pluralSuffix(len(u.recap.commands)-index)))
			break
		}
		lines = append(lines, "  $ "+truncateDisplay(sanitizeText(command), 100))
	}
	if u.recap.outputs > 0 {
		if u.showToolOutput {
			lines = append(lines, fmt.Sprintf("  %d tool output%s visible", u.recap.outputs, pluralSuffix(u.recap.outputs)))
		} else {
			lines = append(lines, "  tool output collapsed · Ctrl+O to expand")
		}
	}
	return strings.Join(lines, "\n")
}

func formatTokenCount(count int64) string {
	if count < 1000 {
		return fmt.Sprintf("%d", count)
	}
	if count < 1000000 {
		return trimDecimal(fmt.Sprintf("%.1fK", float64(count)/1000))
	}
	return trimDecimal(fmt.Sprintf("%.1fM", float64(count)/1000000))
}

func trimDecimal(value string) string {
	value = strings.Replace(value, ".0K", "K", 1)
	return strings.Replace(value, ".0M", "M", 1)
}

func approvalHint(available []string) string {
	if len(available) == 0 {
		return "[a]ccept  [s]ession  [d]ecline  [c]ancel"
	}
	labels := make([]string, 0, len(available))
	for _, decision := range available {
		switch decision {
		case "accept":
			labels = append(labels, "[a]ccept")
		case "acceptForSession":
			labels = append(labels, "[s]ession")
		case "decline":
			labels = append(labels, "[d]ecline")
		case "cancel":
			labels = append(labels, "[c]ancel")
		}
	}
	if len(labels) == 0 {
		return "no supported decision; request will fail closed"
	}
	return strings.Join(labels, "  ")
}

func decisionAllowed(available []string, decision string) bool {
	if len(available) == 0 {
		return true
	}
	for _, candidate := range available {
		if candidate == decision {
			return true
		}
	}
	return false
}

func (u *ui) renderBusy() {
	if u.screenMode {
		return
	}
	u.clearPrompt()
	if u.busy {
		fmt.Fprintf(u.out, "%s  … working · Ctrl-C to interrupt%s\n", muted, reset)
	}
}

func (u *ui) renderPrompt() {
	if u.screenMode {
		return
	}
	if u.approval != nil {
		return
	}
	u.clearPrompt()
	if u.inputRequest == nil && u.busy {
		return
	}
	value := strings.ReplaceAll(u.inputValueWithCursor(), "\n", "\n  ")
	if u.inputRequest != nil {
		question := u.currentInputQuestion()
		label := "?"
		if question.Header != "" {
			label += " " + question.Header
		}
		fmt.Fprintf(u.out, "%s%s%s\n%s› %s%s", cyan, label, reset, cyan, value, reset)
		u.promptLines = strings.Count(value, "\n") + 2
		return
	}
	fmt.Fprintf(u.out, "%s› %s%s", cyan, value, reset)
	u.promptLines = strings.Count(value, "\n") + 1
}

func (u *ui) currentInputQuestion() userInputQuestion {
	if u.inputRequest == nil || u.inputRequest.index >= len(u.inputRequest.questions) {
		return userInputQuestion{}
	}
	return u.inputRequest.questions[u.inputRequest.index]
}

func (u *ui) inputValueWithCursor() string {
	value := append([]rune(nil), u.input...)
	if u.inputRequest != nil && u.currentInputQuestion().IsSecret {
		for index := range value {
			value[index] = '•'
		}
	}
	if u.cursor < 0 {
		u.cursor = 0
	}
	if u.cursor > len(value) {
		u.cursor = len(value)
	}
	value = append(value, 0)
	copy(value[u.cursor+1:], value[u.cursor:])
	value[u.cursor] = cursor
	return string(value)
}

func (u *ui) clearPrompt() {
	if u.screenMode {
		return
	}
	if u.promptLines == 0 {
		return
	}
	fmt.Fprint(u.out, "\r")
	if u.promptLines > 1 {
		fmt.Fprintf(u.out, "\x1b[%dA", u.promptLines-1)
	}
	fmt.Fprint(u.out, "\x1b[J")
	u.promptLines = 0
}

func (u *ui) printStatus(message, color string) {
	if u.screenMode {
		kind := screenStatus
		if color == yellow {
			kind = screenWarning
		}
		if color == red {
			kind = screenError
		}
		if color == green {
			kind = screenStatus
		}
		u.screenAdd(kind, message)
		u.lastOutput = time.Now()
		return
	}
	u.clearPrompt()
	fmt.Fprintf(u.out, "%s  %s%s\n", color, sanitizeText(message), reset)
	u.lastOutput = time.Now()
}

func (u *ui) printError(err error) {
	if u.screenMode {
		u.screenAdd(screenError, err.Error())
		u.lastOutput = time.Now()
		return
	}
	u.printStatus(err.Error(), red)
}

func sanitizeText(value string) string {
	var builder strings.Builder
	for _, character := range value {
		switch character {
		case '\n', '\t':
			builder.WriteRune(character)
		case '\r':
			builder.WriteRune('\n')
		default:
			if character >= 0x20 && character != 0x7f && character != 0x1b {
				builder.WriteRune(character)
			}
		}
	}
	return builder.String()
}

func indentText(value, prefix string) string {
	if value == "" {
		return ""
	}
	return prefix + strings.ReplaceAll(value, "\n", "\n"+prefix)
}

func statusColor(status string) string {
	if strings.Contains(strings.ToLower(status), "fail") || strings.Contains(strings.ToLower(status), "error") || strings.Contains(strings.ToLower(status), "declin") || strings.Contains(strings.ToLower(status), "cancel") {
		return red
	}
	if strings.Contains(strings.ToLower(status), "complete") || strings.Contains(strings.ToLower(status), "accept") || strings.Contains(strings.ToLower(status), "success") {
		return green
	}
	return muted
}

func numberString(value any) string {
	switch value := value.(type) {
	case float64:
		return fmt.Sprintf("%.0f", value)
	case json.Number:
		return value.String()
	case string:
		return value
	default:
		return ""
	}
}

func shortID(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
