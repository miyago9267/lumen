package lumen

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

const cursor = '█'

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

const ctrlCConfirmationWindow = 2 * time.Second

type keyType int

const (
	keyRune keyType = iota
	keyEnter
	keyCtrlEnter
	keyAltEnter
	keyShiftEnter
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
	keyMouseRelease
	keyCtrlC
	keyCtrlX
	keyCtrlO
	keyCtrlU
	keyCtrlBackslash
	keyCtrlD
	keyEscape
	keyTab
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
		case 21:
			keys <- keyEvent{typ: keyCtrlU}
		case 28:
			keys <- keyEvent{typ: keyCtrlBackslash}
		case 4:
			keys <- keyEvent{typ: keyCtrlD}
		case '\r', '\n':
			keys <- keyEvent{typ: keyEnter}
		case 8, 127:
			keys <- keyEvent{typ: keyBackspace}
		case '\t':
			keys <- keyEvent{typ: keyTab}
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
			if key, ok := modifiedEnterKey(fields[1]); ok {
				return keyEvent{typ: key}, true
			}
		}
	}
	if strings.HasSuffix(sequence, "~") {
		fields := strings.Split(strings.TrimSuffix(sequence, "~"), ";")
		if len(fields) == 2 && fields[1] == "13" {
			if key, ok := modifiedEnterKey(fields[0]); ok {
				return keyEvent{typ: key}, true
			}
		}
		if len(fields) == 3 && fields[0] == "27" && fields[2] == "13" {
			if key, ok := modifiedEnterKey(fields[1]); ok {
				return keyEvent{typ: key}, true
			}
		}
	}
	return keyEvent{}, false
}

func modifiedEnterKey(rawModifier string) (keyType, bool) {
	modifier, err := strconv.Atoi(rawModifier)
	if err != nil || modifier < 2 {
		return keyEscape, false
	}
	modifier--
	if modifier&4 != 0 {
		return keyCtrlEnter, true
	}
	if modifier&1 != 0 {
		return keyShiftEnter, true
	}
	if modifier&2 != 0 {
		return keyAltEnter, true
	}
	return keyEscape, false
}

func parseSGRMouse(sequence string) (keyEvent, bool) {
	if len(sequence) < 6 || sequence[0] != '<' || (sequence[len(sequence)-1] != 'M' && sequence[len(sequence)-1] != 'm') {
		return keyEvent{}, false
	}
	release := sequence[len(sequence)-1] == 'm'
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
	if release {
		return keyEvent{typ: keyMouseRelease, mouseX: x, mouseY: y}, true
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
	id        string
	available []string
	action    string
	cwd       string
	reason    string
	// options 非 nil 時為 explicit 模式：沒有預設選項，只能用數字鍵選或取消。
	options []ApprovalOption
	// shownAt 是 card 出現的時間，供 explicit 模式的 typeahead 保護使用。
	shownAt time.Time
}

func (a *approvalRequest) hint() string {
	if a.options == nil {
		return approvalHint(a.available)
	}
	parts := make([]string, 0, len(a.options))
	for index, option := range a.options {
		if index >= 9 {
			break
		}
		parts = append(parts, fmt.Sprintf("[%d] %s", index+1, option.Label))
	}
	return strings.Join(parts, "  ")
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
	id          string
	questions   []userInputQuestion
	answers     map[string]userInputAnswer
	index       int
	optionIndex int
	otherInput  []rune
}

type threadUsage struct {
	totalTokens              int64
	inputTokens              int64
	outputTokens             int64
	cacheReadInputTokens     int64
	cacheCreationInputTokens int64
	modelContextWindow       int64
	hasContextWindow         bool
}

type turnRecap struct {
	commands    []string
	toolCount   int
	outputs     int
	fileChanges int
}

type screenPoint struct {
	row int
	col int
}

type screenSelection struct {
	start    screenPoint
	end      screenPoint
	active   bool
	dragging bool
}

type ui struct {
	backend    Backend
	lastCaps   Capabilities // 連線中斷前最後已知的 backend capability
	thread     threadSummary
	cwd        string
	fullscreen bool
	screenMode bool
	out        io.Writer
	in         io.Reader
	overrides  runtimeOverrides

	input                 []rune
	cursor                int
	promptLines           int
	busy                  bool
	turnID                string
	assistant             bool
	tool                  bool
	toolOutput            bool
	shortcuts             bool
	showToolOutput        bool
	showWelcome           bool
	reasoningActive       bool
	reasoningShown        bool
	reasoningFrame        uint8
	dashboard             bool
	dashboardRows         []threadSummary
	dashboardIndex        int
	dashboardError        string
	approval              *approvalRequest
	inputRequest          *userInputRequest
	inputHistory          []string
	historyIndex          int
	savedInput            []rune
	queuedPrompts         []string
	cancelAndSend         string
	commandPopupIndex     int
	commandPopupDismissed bool
	lastOutput            time.Time
	lastCtrlC             time.Time
	branch                string
	transcript            canonicalTranscript
	screenBlocks          []screenBlock
	activeScreenTurnKey   string
	activeScreenTurnID    string
	screenTurnSequence    int
	toolGroupSequence     int
	toolGroupBoundary     uint64
	expandedToolGroups    map[string]bool
	replyTarget           screenTurnTarget
	usage                 threadUsage
	permissionNote        string         // backend 回報的非 default permission mode 警示（status bar 持續顯示）
	agentCommands         []SlashCommand // backend 動態宣告的 slash commands
	scrollOffset          int
	recap                 turnRecap
	hoverText             string
	hoverRow              int
	selection             screenSelection
	clipboard             func(string) error
	connectionError       string
	connectionStatusEntry string
	reconnectAttempt      int
	reconnectRetryPending bool
	corallineRenderer     corallineRenderer
	corallineLine         string
	corallineSnapshot     string
	corallineError        string
	serverFactory         func() (Backend, error)
	backendPicker         bool
	backendPickerIndex    int
	openBackend           func(name string) (Backend, error) // nil 使用 registry；測試注入
	registry              *backendRegistry                   // 可用 backend（/backend 使用）；nil 表示不支援切換
	clock                 func() time.Time                   // nil 使用 time.Now；測試注入
}

func newUI(backend Backend, thread threadSummary, cwd string, fullscreen bool, overrides runtimeOverrides) *ui {
	return &ui{
		backend:            backend,
		thread:             thread,
		cwd:                cwd,
		fullscreen:         fullscreen,
		overrides:          overrides,
		showWelcome:        fullscreen && len(thread.Turns) == 0,
		historyIndex:       -1,
		out:                os.Stdout,
		in:                 os.Stdin,
		clipboard:          writeClipboard,
		hoverRow:           -1,
		corallineRenderer:  newCorallineRenderer(),
		serverFactory:      startCodexBackend,
		expandedToolGroups: make(map[string]bool),
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
		lifecycle := newInlineTerminalLifecycle(int(os.Stdin.Fd()), u.out)
		if err := lifecycle.Enter(); err != nil {
			return fmt.Errorf("enable raw terminal: %w", err)
		}
		defer lifecycle.Close()
	}

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
		// /backend 會換掉 u.backend，所以每一輪都重新取 Events。
		backend := u.backend
		events := backend.Events()
		select {
		case key, ok := <-keys:
			if !ok {
				return nil
			}
			err := u.safeUIError("handle key", func() error { return u.handleKey(key) })
			if errors.Is(err, errQuit) {
				return nil
			} else if err != nil {
				u.reportUIError(err)
				if !u.busy && u.approval == nil {
					u.renderPrompt()
				}
			}
		case event, ok := <-events:
			if !ok {
				return errors.New("app-server closed")
			}
			if lost, ok := event.(EvConnectionLost); ok {
				if lost.Err != nil {
					u.printError(lost.Err)
					return lost.Err
				}
				return errors.New("app-server closed")
			}
			if reqID, ok := interactiveRequestID(event); ok {
				if !interactive {
					_ = backend.RejectRequest(reqID, "lumen requires an interactive terminal for approval")
					return errors.New("approval requires an interactive terminal")
				}
				if err := u.recoverUI("handle server request", func() { u.handleEvent(event) }); err != nil {
					u.printError(err)
				}
				continue
			}
			if err := u.recoverUI("handle notification", func() { u.handleEvent(event) }); err != nil {
				u.printError(err)
			}
			if !interactive && !u.busy {
				return nil
			}
		}
	}
}

// interactiveRequestID 回傳需要使用者互動才能回覆的 request 的 id。
func interactiveRequestID(event Event) (string, bool) {
	switch event := event.(type) {
	case EvApprovalRequest:
		return event.ReqID, true
	case EvInputRequest:
		return event.ReqID, true
	}
	return "", false
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
	fmt.Fprintf(u.out, "%sEnter%s send · %sAlt+Enter%s newline · %sCtrl-C%s twice quit · %s/help%s · %sinline scrollback fallback%s\n\n", cyan, reset, cyan, reset, cyan, reset, cyan, reset, muted, reset)
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
			turnKey := turn.ID
			if turnKey == "" {
				turnKey = u.newScreenTurnKey()
			}
			for _, item := range turn.Items {
				u.renderScreenHistoryItemForTurn(item, turnKey, turn.ID)
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
	u.renderScreenHistoryItem(item)
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
			if status := toolCompletionStatus(item.Status, item.ExitCode); status != "" {
				status = sanitizeText(status)
				fmt.Fprintf(u.out, "%s  %s%s\n", statusColor(status), status, reset)
			}
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
		return
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
	if key.typ != keyCtrlC {
		u.lastCtrlC = time.Time{}
	}
	if u.screenMode && key.typ == keyCtrlBackslash {
		if u.approval != nil || u.inputRequest != nil {
			return nil
		}
		return u.toggleDashboard()
	}
	if u.screenMode && u.backendPicker && u.approval == nil && u.inputRequest == nil {
		return u.handleBackendPickerKey(key)
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
	if u.screenMode && key.typ == keyCtrlU {
		return u.openDashboard()
	}
	if handled, err := u.handleCommandPopupKey(key); handled {
		return err
	}
	if u.screenMode {
		if key.typ == keyCtrlO {
			u.showToolOutput = !u.showToolOutput
			return nil
		}
		switch key.typ {
		case keyMouseMove:
			width, height := terminalSize()
			if u.selection.dragging {
				u.updateScreenSelection(key.mouseX, key.mouseY, width, height)
				return nil
			}
			u.updateHover(key.mouseX, key.mouseY, width, height)
			return nil
		case keyMouseClick:
			width, height := terminalSize()
			if u.beginScreenSelection(key.mouseX, key.mouseY, width, height) {
				return nil
			}
			u.updateHover(key.mouseX, key.mouseY, width, height)
			u.placeCursorFromMouse(key.mouseX, key.mouseY, width, height)
			return nil
		case keyMouseRelease:
			width, height := terminalSize()
			return u.finishScreenSelection(key.mouseX, key.mouseY, width, height)
		case keyPageUp:
			width, height := terminalSize()
			u.scrollScreenBy(8, width, height)
			return nil
		case keyPageDown:
			width, height := terminalSize()
			u.scrollScreenBy(-8, width, height)
			return nil
		case keyScrollUp:
			width, height := terminalSize()
			u.scrollScreenBy(3, width, height)
			return nil
		case keyScrollDown:
			width, height := terminalSize()
			u.scrollScreenBy(-3, width, height)
			return nil
		}
	}
	if u.busy {
		if key.typ == keyCtrlC {
			return u.handleCtrlC()
		}
		if key.typ == keyCtrlEnter {
			if strings.HasPrefix(strings.TrimSpace(string(u.input)), "/") {
				return u.handleCommand(string(u.input))
			}
			return u.cancelAndSendPrompt()
		}
		if key.typ == keyEnter {
			if strings.HasPrefix(strings.TrimSpace(string(u.input)), "/") {
				return u.handleCommand(string(u.input))
			}
			return u.queuePrompt()
		}
		if key.typ == keyTab {
			return u.queuePrompt()
		}
		if u.editComposer(key) {
			return nil
		}
		return nil
	}

	switch key.typ {
	case keyCtrlC:
		return u.handleCtrlC()
	case keyCtrlD, keyEscape:
		if key.typ == keyEscape && u.replyTarget.key != "" {
			u.clearReplyTarget()
			return nil
		}
		return errQuit
	case keyEnter, keyCtrlEnter:
		value := strings.TrimSpace(string(u.input))
		if value != "" {
			if strings.EqualFold(value, "exit") {
				return errQuit
			}
			if strings.HasPrefix(value, "/") {
				return u.handleCommand(value)
			}
			return u.submit(value)
		}
	case keyAltEnter, keyShiftEnter:
		u.insertRune('\n')
		u.renderPrompt()
	default:
		u.editComposer(key)
	}
	return nil
}

func (u *ui) handleCtrlC() error {
	now := time.Now()
	if !u.lastCtrlC.IsZero() && now.Sub(u.lastCtrlC) <= ctrlCConfirmationWindow {
		u.lastCtrlC = time.Time{}
		return errQuit
	}
	u.lastCtrlC = now

	message := "press Ctrl-C again to exit"
	if u.busy {
		message = "interrupt requested · press Ctrl-C again to exit"
		if backend := u.backend; u.turnID != "" && backend != nil {
			u.printStatus(message, yellow)
			return backend.Cancel(u.thread.ID, u.turnID)
		}
	}
	u.printStatus(message, yellow)
	if !u.screenMode {
		u.renderPrompt()
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
	case keyAltEnter, keyShiftEnter:
		u.insertRune('\n')
	default:
		return false
	}
	u.renderPrompt()
	return true
}

func (u *ui) handleCommand(value string) error {
	if strings.EqualFold(strings.TrimSpace(value), "exit") {
		return errQuit
	}
	name, args, ok := slashCommandInput(value)
	if !ok {
		return nil
	}
	command, ok := u.slashCommandByName(name)
	if !ok {
		if _, known := slashCommandByName(name); known && u.dynamicCatalog() {
			u.clearCommandInput()
			u.commandMessage("/"+name+" is not supported by backend "+u.backend.Name(), muted)
			return nil
		}
		return fmt.Errorf("unknown command %q; try /help", "/"+name)
	}
	if command.agent {
		if u.busy {
			return u.queuePrompt()
		}
		text := strings.TrimSpace(value)
		u.clearCommandInput()
		return u.submit(text)
	}
	u.clearCommandInput()
	switch command.name {
	case "help":
		u.showHelp()
	case "status":
		u.showStatus()
	case "dashboard":
		return u.openDashboard()
	case "backend":
		return u.commandBackend(args)
	case "quit":
		return errQuit
	default:
		return u.handleNativeCommand(command, args)
	}
	return nil
}

func (u *ui) clearCommandInput() {
	u.clearPrompt()
	u.input = nil
	u.cursor = 0
	u.commandPopupIndex = 0
	u.commandPopupDismissed = false
}

func (u *ui) showHelp() {
	u.showWelcome = false
	u.clearPrompt()
	u.input = nil
	u.cursor = 0
	if u.screenMode {
		u.screenAdd(screenInfo, u.slashCommandHelpText())
		return
	}
	fmt.Fprintf(u.out, "\n%s%s%s\n", bold, u.slashCommandHelpText(), reset)
	u.renderPrompt()
}

func (u *ui) showStatus() {
	u.showWelcome = false
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
	backend := u.backend
	if backend == nil {
		return errors.New("app-server is reconnecting; prompt was not sent")
	}
	u.showWelcome = false
	u.clearSelection()
	u.clearReplyTarget()
	u.clearPrompt()
	u.rememberPrompt(value)
	u.input = nil
	u.cursor = 0
	u.assistant = false
	u.tool = false
	u.toolOutput = false
	u.reasoningActive = false
	u.reasoningShown = false
	u.reasoningFrame = 0
	u.recap = turnRecap{}
	u.beginScreenTurn("")
	u.screenAdd(screenUser, value)
	if !u.screenMode {
		fmt.Fprintf(u.out, "%s› you%s\n%s%s%s\n", bold, reset, muted, sanitizeText(value), reset)
	}
	u.busy = true
	if !u.screenMode {
		fmt.Fprintf(u.out, "%s  … working%s%s\n", muted, reset, "")
	}
	turnID, err := backend.Prompt(u.thread.ID, value, u.overrides)
	if err != nil {
		u.busy = false
		u.renderPrompt()
		return err
	}
	u.turnID = turnID
	u.bindActiveScreenTurn(turnID)
	return nil
}

// handleEvent 是 UI 處理 backend event 的唯一入口。
func (u *ui) handleEvent(event Event) {
	switch event := event.(type) {
	case EvTurnStarted:
		u.turnID = event.TurnID
		u.bindActiveScreenTurn(event.TurnID)
	case EvAssistantDelta:
		u.reasoningActive = false
		u.screenAppend(screenAssistant, event.Text)
		if u.screenMode {
			u.assistant = true
			u.lastOutput = time.Now()
			return
		}
		if !u.assistant {
			u.clearPrompt()
			fmt.Fprintf(u.out, "\n%s› assistant%s ", green, reset)
			u.assistant = true
		}
		fmt.Fprint(u.out, sanitizeText(event.Text))
		u.lastOutput = time.Now()
	case EvReasoningStarted:
		if u.reasoningShown {
			return
		}
		u.reasoningShown = true
		u.reasoningActive = true
		u.reasoningFrame = 0
		if u.screenMode {
			return
		}
		u.clearPrompt()
		fmt.Fprintf(u.out, "\n%s  … reasoning%s\n", muted, reset)
	case EvReasoningCompleted:
		u.reasoningActive = false
	case EvReasoningDelta:
		// 預設不顯示 reasoning 文字，只靠 EvReasoningStarted 維持 thinking 狀態。
	case EvToolStarted:
		u.reasoningActive = false
		u.recap.toolCount++
		if event.Kind == ToolEdit {
			u.recap.fileChanges++
		}
		u.toolOutput = false
		if event.Command != "" {
			u.recap.commands = append(u.recap.commands, event.Command)
		}
		u.screenAdd(screenTool, event.Title)
		if u.screenMode {
			u.tool = true
			u.toolOutput = false
			return
		}
		u.clearPrompt()
		fmt.Fprintf(u.out, "\n%s  %s%s\n", yellow, sanitizeText(event.Title), reset)
		u.tool = true
	case EvToolOutput:
		u.reasoningActive = false
		firstOutput := !u.toolOutput
		if firstOutput {
			u.recap.outputs++
			u.toolOutput = true
			u.screenAdd(screenTool, "output")
		}
		u.screenAppend(screenTool, event.Text)
		if u.screenMode {
			u.tool = true
			u.lastOutput = time.Now()
			return
		}
		if !u.tool {
			u.clearPrompt()
			fmt.Fprintf(u.out, "\n%s  output%s\n", muted, reset)
			u.tool = true
		}
		fmt.Fprint(u.out, indentText(sanitizeText(event.Text), "    "))
		u.lastOutput = time.Now()
	case EvToolCompleted:
		u.reasoningActive = false
		if event.Label == "" {
			return
		}
		u.screenAddToolActivityStatus(event.Label, u.activeScreenTurnKey, u.activeScreenTurnID)
		if u.screenMode {
			return
		}
		u.clearPrompt()
		fmt.Fprintf(u.out, "%s  %s%s\n", statusColor(event.Label), sanitizeText(event.Label), reset)
	case EvPlanUpdated:
		u.reasoningActive = false
		var planLines []string
		for _, item := range event.Items {
			planLines = append(planLines, fmt.Sprintf("[%s] %s", item.Status, item.Text))
		}
		u.screenAdd(screenPlan, strings.Join(planLines, "\n"))
		if u.screenMode {
			return
		}
		u.clearPrompt()
		fmt.Fprintf(u.out, "\n%s  plan%s", cyan, reset)
		for _, item := range event.Items {
			fmt.Fprintf(u.out, "\n    [%s] %s", sanitizeText(item.Status), sanitizeText(item.Text))
		}
		fmt.Fprintln(u.out)
	case EvSessionTitle:
		u.thread.Name = event.Title
	case EvUsage:
		u.usage.totalTokens = event.Used
		u.usage.inputTokens, u.usage.outputTokens = 0, 0
		u.usage.cacheReadInputTokens, u.usage.cacheCreationInputTokens = 0, 0
		if event.Breakdown != nil {
			u.usage.inputTokens = event.Breakdown.Input
			u.usage.outputTokens = event.Breakdown.Output
			u.usage.cacheReadInputTokens = event.Breakdown.CacheRead
			u.usage.cacheCreationInputTokens = event.Breakdown.CacheCreation
		}
		if event.Size != nil {
			u.usage.modelContextWindow = *event.Size
			u.usage.hasContextWindow = true
		}
	case EvModeWarning:
		changed := u.permissionNote != event.Text
		u.permissionNote = event.Text
		if changed && event.Text != "" && !u.screenMode {
			u.printStatus(event.Text, yellow)
		}
	case EvCommandsUpdated:
		u.agentCommands = event.Commands
	case EvTurnCompleted:
		u.finishTurn(event.Label)
	case EvNotice:
		switch event.Level {
		case NoticeError:
			u.printError(errors.New(event.Text))
		case NoticeWarn:
			u.printStatus(event.Text, yellow)
		default:
			u.printStatus(event.Text, muted)
		}
	case EvApprovalRequest:
		u.presentApproval(event)
	case EvApprovalExpired:
		if u.approval == nil || u.approval.id != event.ReqID {
			return
		}
		u.approval = nil
		u.printStatus(event.Text, yellow)
		if !u.screenMode {
			u.renderBusy()
		}
	case EvInputRequest:
		u.presentInputRequest(event)
	case EvRequestRejected:
		if u.screenMode {
			u.screenAdd(screenError, "request rejected · "+event.Message)
			return
		}
		u.clearPrompt()
		fmt.Fprintf(u.out, "\n%s  request rejected · %s%s\n", red, sanitizeText(event.Message), reset)
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
	u.reasoningActive = false
	u.reasoningShown = false
	u.reasoningFrame = 0
	u.busy = false
	u.turnID = ""
	if status == "" {
		status = "completed"
	}
	u.screenAdd(screenInfo, u.recapText(status))
	u.screenAdd(screenStatus, "turn "+status)
	u.endScreenTurn()
	if u.screenMode {
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

func (u *ui) presentApproval(event EvApprovalRequest) {
	available := make([]string, 0, len(event.Options))
	for _, option := range event.Options {
		available = append(available, option.ID)
	}
	var options []ApprovalOption
	if event.Explicit {
		options = append(make([]ApprovalOption, 0, len(event.Options)), event.Options...)
	}
	u.showApprovalRequest(event.ReqID, event.Title, valueOr(event.CWD, u.cwd), event.Reason, available, options)
}

func (u *ui) showApproval(reqID, action, cwd, reason string, available []string) {
	u.showApprovalRequest(reqID, action, cwd, reason, available, nil)
}

func (u *ui) showApprovalRequest(reqID, action, cwd, reason string, available []string, options []ApprovalOption) {
	u.toolGroupBoundary++
	u.clearPrompt()
	u.dashboard = false
	u.dashboardError = ""
	u.approval = &approvalRequest{id: reqID, available: available, action: action, cwd: cwd, reason: reason, options: options, shownAt: u.now()}
	if u.screenMode {
		return
	}
	fmt.Fprintf(u.out, "\n%s%s  approval required%s\n", bold, yellow, reset)
	fmt.Fprintf(u.out, "%s  action%s  %s\n", muted, reset, sanitizeText(action))
	fmt.Fprintf(u.out, "%s  cwd%s     %s\n", muted, reset, sanitizeText(cwd))
	if reason != "" {
		fmt.Fprintf(u.out, "%s  reason%s   %s\n", muted, reset, sanitizeText(reason))
	}
	fmt.Fprintf(u.out, "%s  %s%s\n", cyan, u.approval.hint(), reset)
}

func (u *ui) presentInputRequest(event EvInputRequest) {
	if len(event.Questions) == 0 {
		if backend := u.backend; backend != nil {
			_ = backend.AnswerInput(event.ReqID, map[string][]string{})
		}
		u.printStatus("user input request had no questions", yellow)
		return
	}
	u.toolGroupBoundary++
	u.clearPrompt()
	u.input = nil
	u.cursor = 0
	u.dashboard = false
	u.dashboardError = ""
	u.inputRequest = &userInputRequest{
		id:        event.ReqID,
		questions: event.Questions,
		answers:   make(map[string]userInputAnswer, len(event.Questions)),
	}
	u.resetInputQuestion()
	u.printStatus(fmt.Sprintf("input requested · %d question%s", len(event.Questions), pluralSuffix(len(event.Questions))), cyan)
	u.renderPrompt()
}

// approvalTypeaheadGuard 是 explicit approval card 出現後忽略數字鍵的時間：
// 使用者在 card 出現前已打下的數字（typeahead）不得因此選到 allow_*。取消鍵不受限制。
const approvalTypeaheadGuard = 400 * time.Millisecond

func (u *ui) now() time.Time {
	if u.clock != nil {
		return u.clock()
	}
	return time.Now()
}

// handleExplicitApprovalKey 處理沒有預設選項的 approval（ACP）：Enter 不做決定。
func (u *ui) handleExplicitApprovalKey(key keyEvent) error {
	approval := u.approval
	optionID, label := "", ""
	switch key.typ {
	case keyCtrlC, keyEscape:
		optionID, label = CancelApprovalID, "cancelled"
	case keyRune:
		if key.rune >= '1' && key.rune <= '9' {
			if u.now().Sub(approval.shownAt) < approvalTypeaheadGuard {
				return nil
			}
			if index := int(key.rune - '1'); index < len(approval.options) {
				optionID, label = approval.options[index].ID, approval.options[index].Label
			}
		}
	}
	if optionID == "" {
		return nil
	}
	u.approval = nil
	backend := u.backend
	if backend == nil {
		return errors.New("backend unavailable")
	}
	if err := backend.ResolveApproval(approval.id, optionID); err != nil {
		return err
	}
	if u.screenMode {
		u.screenAdd(screenStatus, label)
		return nil
	}
	fmt.Fprintf(u.out, "%s  %s%s\n", statusColor(label), sanitizeText(label), reset)
	u.renderBusy()
	return nil
}

func (u *ui) handleApprovalKey(key keyEvent) error {
	if u.approval.options != nil {
		return u.handleExplicitApprovalKey(key)
	}
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
	backend := u.backend
	if backend == nil {
		return errors.New("app-server unavailable")
	}
	if err := backend.ResolveApproval(approval.id, decision); err != nil {
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
		if u.inputQuestionHasOptions() && !u.inputOptionIsOther() {
			if key.rune >= '1' && key.rune <= '9' {
				u.setInputOption(int(key.rune - '1'))
				u.renderPrompt()
				return nil
			}
			if u.currentInputQuestion().IsOther && (key.rune == 'o' || key.rune == 'O') {
				u.setInputOption(len(u.currentInputQuestion().Options))
				u.renderPrompt()
				return nil
			}
			if u.currentInputQuestion().IsOther {
				u.setInputOption(len(u.currentInputQuestion().Options))
				u.insertRune(key.rune)
				u.renderPrompt()
				return nil
			}
			return nil
		}
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
		if u.moveInputOption(-1) {
			u.renderPrompt()
			return nil
		}
		u.moveVertical(-1)
		u.renderPrompt()
	case keyDown:
		if u.moveInputOption(1) {
			u.renderPrompt()
			return nil
		}
		u.moveVertical(1)
		u.renderPrompt()
	case keyAltEnter, keyShiftEnter:
		if !u.inputQuestionHasOptions() || u.inputOptionIsOther() {
			u.insertRune('\n')
			u.renderPrompt()
		}
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
	request.answers[question.ID] = userInputAnswer{Answers: []string{u.inputAnswer()}}
	request.index++
	u.resetInputQuestion()
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
	answers := make(map[string][]string, len(request.answers))
	for id, answer := range request.answers {
		answers[id] = answer.Answers
	}
	backend := u.backend
	if backend == nil {
		return errors.New("app-server unavailable")
	}
	if err := backend.AnswerInput(request.id, answers); err != nil {
		return err
	}
	if cancel {
		u.printStatus("input canceled", yellow)
		if interrupt && u.turnID != "" {
			return backend.Cancel(u.thread.ID, u.turnID)
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
	if u.showToolOutput {
		for index, command := range u.recap.commands {
			if index == 3 {
				lines = append(lines, fmt.Sprintf("  +%d more command%s", len(u.recap.commands)-index, pluralSuffix(len(u.recap.commands)-index)))
				break
			}
			lines = append(lines, "  $ "+truncateDisplay(sanitizeText(command), 100))
		}
	}
	if u.recap.outputs > 0 {
		if u.showToolOutput {
			lines = append(lines, fmt.Sprintf("  %d tool output%s visible", u.recap.outputs, pluralSuffix(u.recap.outputs)))
		} else if !u.screenMode {
			lines = append(lines, fmt.Sprintf("  %d tool output%s hidden", u.recap.outputs, pluralSuffix(u.recap.outputs)))
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
		fmt.Fprintf(u.out, "%s  … working · Ctrl-C interrupt · twice quit%s\n", muted, reset)
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
	if u.inputRequest != nil {
		details := u.inputQuestionDetails()
		promptLines := 0
		for index, detail := range details {
			promptLines += strings.Count(detail, "\n") + 1
			if index == 0 {
				fmt.Fprintf(u.out, "%s%s%s", cyan, detail, reset)
			} else {
				fmt.Fprintf(u.out, "\n%s  %s%s", cyan, strings.ReplaceAll(detail, "\n", "\n  "), reset)
			}
		}
		u.promptLines = promptLines
		return
	}
	value := strings.ReplaceAll(u.inputValueWithCursor(), "\n", "\n  ")
	fmt.Fprintf(u.out, "%s› %s%s", cyan, value, reset)
	u.promptLines = strings.Count(value, "\n") + 1
	if popupLines := u.inlineCommandPopupLines(); popupLines > 0 {
		items := u.commandPopupItems()
		selected := u.commandPopupIndex
		if selected < 0 {
			selected = 0
		}
		if selected >= len(items) {
			selected = len(items) - 1
		}
		fmt.Fprintf(u.out, "\n%s%s%s", cyan, u.slashCommandsTitle(), reset)
		for index, command := range items {
			if index == 6 {
				break
			}
			marker := "  "
			if index == selected {
				marker = "› "
			}
			fmt.Fprintf(u.out, "\n%s%s/%-18s  %s%s", muted, marker, command.name, command.description, reset)
		}
		fmt.Fprintf(u.out, "\n%s↑↓ choose · Tab complete · Enter run · Esc close%s", cyan, reset)
		u.promptLines += popupLines
	}
}

func (u *ui) currentInputQuestion() userInputQuestion {
	if u.inputRequest == nil || u.inputRequest.index >= len(u.inputRequest.questions) {
		return userInputQuestion{}
	}
	return u.inputRequest.questions[u.inputRequest.index]
}

func (u *ui) inputQuestionHasOptions() bool {
	question := u.currentInputQuestion()
	return len(question.Options) > 0 || question.IsOther
}

func (u *ui) inputOptionCount() int {
	question := u.currentInputQuestion()
	count := len(question.Options)
	if question.IsOther {
		count++
	}
	return count
}

func (u *ui) inputOptionIndex() int {
	count := u.inputOptionCount()
	if count == 0 || u.inputRequest == nil {
		return -1
	}
	if u.inputRequest.optionIndex < 0 || u.inputRequest.optionIndex >= count {
		return 0
	}
	return u.inputRequest.optionIndex
}

func (u *ui) inputOptionIsOther() bool {
	question := u.currentInputQuestion()
	return question.IsOther && u.inputOptionIndex() == len(question.Options)
}

func (u *ui) setInputOption(index int) bool {
	if u.inputRequest == nil || !u.inputQuestionHasOptions() {
		return false
	}
	if index < 0 || index >= u.inputOptionCount() {
		return false
	}
	if u.inputOptionIsOther() {
		u.inputRequest.otherInput = append([]rune(nil), u.input...)
	}
	u.inputRequest.optionIndex = index
	if u.inputOptionIsOther() {
		u.input = append([]rune(nil), u.inputRequest.otherInput...)
		u.cursor = len(u.input)
	} else {
		u.input = nil
		u.cursor = 0
	}
	return true
}

func (u *ui) moveInputOption(direction int) bool {
	if !u.inputQuestionHasOptions() {
		return false
	}
	index := u.inputOptionIndex() + direction
	if index < 0 {
		index = 0
	}
	if index >= u.inputOptionCount() {
		index = u.inputOptionCount() - 1
	}
	return u.setInputOption(index)
}

func (u *ui) resetInputQuestion() {
	u.input = nil
	u.cursor = 0
	if u.inputRequest == nil {
		return
	}
	u.inputRequest.optionIndex = 0
	u.inputRequest.otherInput = nil
}

func (u *ui) inputAnswer() string {
	if u.inputQuestionHasOptions() && !u.inputOptionIsOther() {
		index := u.inputOptionIndex()
		question := u.currentInputQuestion()
		if index >= 0 && index < len(question.Options) {
			return question.Options[index].Label
		}
	}
	return string(u.input)
}

func (u *ui) inputQuestionDetails() []string {
	question := u.currentInputQuestion()
	details := []string{
		fmt.Sprintf("? question %d/%d", u.inputRequest.index+1, len(u.inputRequest.questions)),
		question.Question,
	}
	if question.Header != "" {
		details[0] += " · " + question.Header
	}
	if !u.inputQuestionHasOptions() {
		details = append(details, "answer: "+u.inputValueWithCursor())
		return details
	}
	selected := u.inputOptionIndex()
	for index, option := range question.Options {
		marker := "  "
		if index == selected {
			marker = "› "
		}
		label := fmt.Sprintf("%s%d) %s", marker, index+1, option.Label)
		if option.Description != "" {
			label += " — " + option.Description
		}
		details = append(details, label)
	}
	if question.IsOther {
		marker := "  "
		if u.inputOptionIsOther() {
			marker = "› "
		}
		details = append(details, marker+"o) Other")
	}
	if u.inputOptionIsOther() {
		details = append(details, "other: "+u.inputValueWithCursor())
	} else {
		details = append(details, "↑↓ select · 1–9 choose · o Other · Enter submit")
	}
	return details
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
	if u.screenMode {
		return string(value)
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
	kind := screenStatus
	if color == yellow {
		kind = screenWarning
	}
	if color == red {
		kind = screenError
	}
	if u.screenMode {
		u.showWelcome = false
		u.screenAdd(kind, message)
		u.lastOutput = time.Now()
		return
	}
	u.screenAdd(kind, message)
	u.clearPrompt()
	fmt.Fprintf(u.out, "%s  %s%s\n", color, sanitizeText(message), reset)
	u.lastOutput = time.Now()
}

func (u *ui) printError(err error) {
	if u.screenMode {
		u.showWelcome = false
		u.screenAdd(screenError, err.Error())
		u.lastOutput = time.Now()
		return
	}
	u.screenAdd(screenError, err.Error())
	u.clearPrompt()
	fmt.Fprintf(u.out, "%s  %s%s\n", red, sanitizeText(err.Error()), reset)
	u.lastOutput = time.Now()
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
