package lumen

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

type screenBlockKind uint8

const (
	screenEvent screenBlockKind = iota
	screenUser
	screenAssistant
	screenTool
	screenPlan
	screenStatus
	screenInfo
	screenWarning
	screenError
	screenCode
	screenDiagram
)

type screenBlock struct {
	kind         screenBlockKind
	text         string
	turnKey      string
	turnID       string
	toolActivity bool
	toolGroup    string
}

const maxScreenBlocks = 256

const screenComposerGapRows = 1

func maxScreenComposerLines(height int) int {
	maxLines := height - 4 - screenComposerGapRows
	if maxLines < 2 {
		maxLines = 2
	}
	return maxLines
}

func (u *ui) runFullscreen(initialPrompt string) error {
	lifecycle := newFullscreenTerminalLifecycle(int(os.Stdin.Fd()), u.out)
	if err := lifecycle.Enter(); err != nil {
		return fmt.Errorf("enable raw terminal: %w", err)
	}
	defer lifecycle.Close()
	defer func() {
		if u.client != nil {
			u.client.Close()
		}
	}()
	supervisor := newSessionSupervisor(u.serverFactory)
	defer supervisor.Close()

	u.screenMode = true
	u.showWelcome = initialPrompt == "" && len(u.thread.Turns) == 0
	u.branch = gitBranch(u.cwd)

	u.screenAdd(screenEvent, "session_start  [app-server]")
	u.renderHistory()
	keyEvents := make(chan keyEvent, 8)
	go readKeys(u.in, keyEvents)
	resizeEvents := make(chan os.Signal, 1)
	signal.Notify(resizeEvents, syscall.SIGWINCH)
	defer signal.Stop(resizeEvents)
	dirty := false
	renderWake := make(chan struct{}, 1)
	invalidate := func() {
		dirty = true
		select {
		case renderWake <- struct{}{}:
		default:
		}
	}
	var renderTicks <-chan time.Time
	var reasoningTimer *time.Timer
	setReasoningTimer := func() {
		if reasoningTimer != nil {
			reasoningTimer.Stop()
		}
		renderTicks = nil
		if u.reasoningActive {
			reasoningTimer = time.NewTimer(166 * time.Millisecond)
			renderTicks = reasoningTimer.C
		}
	}
	defer func() {
		if reasoningTimer != nil {
			reasoningTimer.Stop()
		}
	}()

	var retryResults <-chan time.Time
	var retryTimer *time.Timer
	reconnectAttempt := 0
	startReconnect := func() {
		supervisor.Start(reconnectSnapshot{
			threadID:             u.thread.ID,
			cwd:                  u.cwd,
			overrides:            u.overrides,
			hasTranscriptContent: u.hasTranscriptContent(),
		})
	}
	scheduleReconnect := func() {
		if retryTimer != nil {
			retryTimer.Stop()
		}
		retryTimer = time.NewTimer(reconnectDelay(reconnectAttempt))
		retryResults = retryTimer.C
	}
	defer func() {
		if retryTimer != nil {
			retryTimer.Stop()
		}
	}()
	handleDisconnect := func(client *appServer, cause error) {
		if client == nil || u.client != client {
			return
		}
		if cause == nil {
			cause = client.failureError()
		}
		client.Close()
		u.markConnectionLost(cause)
		reconnectAttempt = 0
		startReconnect()
		invalidate()
	}
	if initialPrompt != "" {
		initialClient := u.client
		if err := u.submit(initialPrompt); err != nil {
			u.input = []rune(initialPrompt)
			u.cursor = len(u.input)
			connectionFailed := initialClient != nil && initialClient.failureError() != nil
			if !connectionFailed && initialClient != nil {
				select {
				case <-initialClient.closed:
					connectionFailed = true
				default:
				}
			}
			if connectionFailed {
				handleDisconnect(initialClient, initialClient.failureError())
			} else {
				u.reportUIError(err)
			}
		}
	} else {
		u.renderPrompt()
	}
	setReasoningTimer()
	if err := u.recoverUI("initial render", u.renderScreen); err != nil {
		u.reportUIError(err)
	}
	dirty = false

	for {
		client := u.client
		var requests <-chan serverRequest
		var notifications <-chan rpcMessage
		var clientErrors <-chan error
		var clientClosed <-chan struct{}
		if client != nil {
			requests = client.requests
			notifications = client.notifications
			clientErrors = client.errors
			clientClosed = client.closed
		}
		select {
		case key, ok := <-keyEvents:
			if !ok {
				return nil
			}
			err := u.safeUIError("handle key", func() error { return u.handleKey(key) })
			if errors.Is(err, errQuit) {
				return nil
			} else if err != nil {
				u.reportUIError(err)
			}
			setReasoningTimer()
			invalidate()
		case request, ok := <-requests:
			if !ok {
				handleDisconnect(client, nil)
				continue
			}
			if err := u.recoverUI("handle server request", func() { u.handleServerRequest(request) }); err != nil {
				u.reportUIError(err)
			}
			setReasoningTimer()
			invalidate()
		case notification, ok := <-notifications:
			if !ok {
				handleDisconnect(client, nil)
				continue
			}
			if err := u.recoverUI("handle notification", func() { u.handleNotification(notification) }); err != nil {
				u.reportUIError(err)
			}
			setReasoningTimer()
			invalidate()
		case clientError, ok := <-clientErrors:
			if !ok {
				handleDisconnect(client, nil)
				continue
			}
			handleDisconnect(client, clientError)
			setReasoningTimer()
		case <-clientClosed:
			handleDisconnect(client, nil)
			setReasoningTimer()
		case result := <-supervisor.Results():
			if result.err != nil {
				reconnectAttempt++
				u.markReconnectFailed(result.err, reconnectAttempt)
				scheduleReconnect()
				invalidate()
			} else if result.client == nil {
				reconnectAttempt++
				u.markReconnectFailed(errors.New("reconnect returned no app-server"), reconnectAttempt)
				scheduleReconnect()
				invalidate()
			} else {
				u.adoptReconnected(result.client, result.thread)
				reconnectAttempt = 0
				invalidate()
			}
		case <-retryResults:
			retryResults = nil
			u.reconnectRetryPending = false
			invalidate()
			if u.client == nil {
				startReconnect()
			}
		case <-renderWake:
			if dirty {
				if err := u.recoverUI("render screen", u.renderScreen); err != nil {
					u.reportUIError(err)
				}
				dirty = false
			}
		case <-resizeEvents:
			invalidate()
		case <-renderTicks:
			if u.reasoningActive {
				u.reasoningFrame++
				dirty = true
			}
			setReasoningTimer()
			if dirty {
				if err := u.recoverUI("render screen", u.renderScreen); err != nil {
					u.reportUIError(err)
				}
				dirty = false
			}
		}
	}
}

func (u *ui) renderScreen() {
	width, height := terminalSize()
	fmt.Fprint(u.out, "\x1b[?25l\x1b[H", u.screenFrame(width, height))
	if row, column, ok := u.screenCursorPosition(width, height); ok {
		fmt.Fprintf(u.out, "\x1b[%d;%dH\x1b[?25h", row, column)
	}
}

func terminalSize() (int, int) {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width < 20 || height < 8 {
		return 100, 24
	}
	return width, height
}

func (u *ui) screenComposerAndBodyRows(width, height int) ([]string, int) {
	composer := u.screenComposerLines(width)
	composer = trimComposerLines(composer, maxScreenComposerLines(height))
	bodyRows := height - 3 - screenComposerGapRows - len(composer)
	if bodyRows < 1 {
		bodyRows = 1
	}
	return composer, bodyRows
}

func clampScreenScrollOffset(offset, transcriptRowCount, bodyRows int) int {
	maxOffset := transcriptRowCount - bodyRows
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset < 0 {
		return 0
	}
	if offset > maxOffset {
		return maxOffset
	}
	return offset
}

func (u *ui) scrollScreenBy(delta, width, height int) {
	if width < 20 {
		width = 20
	}
	if height < 8 {
		height = 8
	}
	if u.screenShowsWelcome() {
		u.scrollOffset = 0
		return
	}
	_, bodyRows := u.screenComposerAndBodyRows(width, height)
	rows := u.screenTranscriptRows(width)
	u.scrollOffset = clampScreenScrollOffset(u.scrollOffset+delta, len(rows), bodyRows)
}

func (u *ui) screenFrame(width, height int) string {
	if width < 20 {
		width = 20
	}
	if height < 8 {
		height = 8
	}

	composer, bodyRows := u.screenComposerAndBodyRows(width, height)

	transcriptRows := u.screenTranscriptRows(width)
	transcript := make([]string, 0, bodyRows)
	if u.screenShowsWelcome() {
		u.scrollOffset = 0
		if len(transcriptRows) > bodyRows {
			transcriptRows = transcriptRows[:bodyRows]
		}
		for index := 0; index < (bodyRows-len(transcriptRows))/2; index++ {
			transcript = append(transcript, "")
		}
		for index, row := range transcriptRows {
			transcript = append(transcript, u.screenRenderedRow(row, index, width))
		}
	} else {
		u.scrollOffset = clampScreenScrollOffset(u.scrollOffset, len(transcriptRows), bodyRows)
		end := len(transcriptRows) - u.scrollOffset
		if end < 0 {
			end = 0
		}
		if end > len(transcriptRows) {
			end = len(transcriptRows)
		}
		start := end - bodyRows
		if start < 0 {
			start = 0
		}
		if start > end {
			start = end
		}
		for index, row := range transcriptRows[start:end] {
			transcript = append(transcript, u.screenRenderedRow(row, start+index, width))
		}
	}
	for len(transcript) < bodyRows {
		transcript = append(transcript, "")
	}

	lines := []string{
		u.screenHeaderLine(width),
		screenStyledLine(screenEvent, strings.Repeat("┈", width), width),
	}
	lines = append(lines, transcript...)
	for index := 0; index < screenComposerGapRows; index++ {
		lines = append(lines, "")
	}
	lines = append(lines, composer...)
	lines = append(lines, u.screenDockLine(width))

	var builder strings.Builder
	for index, line := range lines {
		if index > 0 {
			builder.WriteString("\r\n")
		}
		builder.WriteString("\x1b[K")
		builder.WriteString(line)
	}
	return builder.String()
}

func trimComposerLines(lines []string, maxLines int) []string {
	if len(lines) <= maxLines {
		return lines
	}
	if maxLines < 2 {
		return lines[:1]
	}
	trimmed := append([]string(nil), lines[:maxLines-1]...)
	return append(trimmed, lines[len(lines)-1])
}

func (u *ui) screenHeaderLine(width int) string {
	branch := valueOr(u.branch, "detached")
	left := fmt.Sprintf("⟡ %s  %s", branch, displayCWD(u.cwd))
	if name := strings.TrimSpace(u.thread.Name); name != "" {
		left += " · " + sanitizeText(name)
	}
	right := fmt.Sprintf("%s · %s", u.effectiveModel(), u.effectiveEffort())
	right += " · " + u.effectiveApproval() + " · " + u.effectiveSandbox()
	if usage := u.usageLabel(); usage != "" {
		right = usage + "  " + right
	}
	if displayWidth(left)+displayWidth(right)+1 > width {
		left = truncateDisplay(left, width-displayWidth(right)-1)
	}
	if displayWidth(left)+displayWidth(right)+1 > width {
		right = truncateDisplay(right, width-displayWidth(left)-1)
	}
	gap := width - displayWidth(left) - displayWidth(right)
	if gap < 1 {
		gap = 1
	}
	return screenStyledLine(screenEvent, left+strings.Repeat(" ", gap)+right, width)
}

func (u *ui) usageLabel() string {
	if u.usage.totalTokens <= 0 && !u.usage.hasContextWindow {
		return ""
	}
	if u.usage.hasContextWindow && u.usage.modelContextWindow > 0 {
		return formatTokenCount(u.usage.totalTokens) + " / " + formatTokenCount(u.usage.modelContextWindow)
	}
	return formatTokenCount(u.usage.totalTokens)
}

func (u *ui) renderScreenHistoryItem(item historyItem) {
	u.renderScreenHistoryItemForTurn(item, "", "")
}

func (u *ui) renderScreenHistoryItemForTurn(item historyItem, turnKey, turnID string) {
	u.renderScreenHistoryItemForTurnMode(item, turnKey, turnID, false)
}

func (u *ui) renderScreenHistoryItemForTurnMode(item historyItem, turnKey, turnID string, replay bool) {
	add := func(kind screenBlockKind, text string) {
		if replay {
			u.screenReplayAddForTurn(kind, text, turnKey, turnID)
			return
		}
		u.screenAddForTurn(kind, text, turnKey, turnID)
	}
	addToolStatus := func(text string) {
		if replay {
			u.screenReplayAddToolActivityStatus(text, turnKey, turnID)
			return
		}
		u.screenAddToolActivityStatus(text, turnKey, turnID)
	}
	switch item.Type {
	case "userMessage":
		if text := historyText(item.Content); text != "" {
			add(screenUser, text)
		}
	case "agentMessage":
		if item.Text != "" {
			add(screenAssistant, item.Text)
		}
	case "commandExecution":
		if item.Command != "" {
			add(screenTool, "$ "+item.Command)
		}
		if item.AggregatedOutput != "" {
			add(screenTool, "output\n"+item.AggregatedOutput)
		}
		if item.Status != "" {
			if status := toolCompletionStatus(item.Status, item.ExitCode); status != "" {
				addToolStatus(status)
			}
		}
	case "fileChange":
		if item.Status != "" {
			addToolStatus("file change " + item.Status)
		}
	case "mcpToolCall":
		add(screenTool, fmt.Sprintf("◇ %s/%s", item.Server, item.Tool))
	case "plan":
		if item.Text != "" {
			add(screenPlan, item.Text)
		}
	case "reasoning":
		return
	}
}

func (u *ui) screenReplayAddForTurn(kind screenBlockKind, text, turnKey, turnID string) {
	text = strings.TrimRight(sanitizeText(text), "\n")
	if strings.TrimSpace(text) == "" {
		return
	}
	turnKey = u.replayTurnKey(turnKey, turnID)
	for index := range u.transcript.entries {
		entry := &u.transcript.entries[index]
		if entry.turnKey != turnKey || entry.turnID != turnID || entry.kind != kind {
			continue
		}
		if entry.text == text {
			return
		}
		if (kind == screenAssistant || kind == screenTool) && entry.text != "" && strings.HasPrefix(text, entry.text) {
			entry.text = text
			u.syncScreenProjection()
			return
		}
	}
	before := len(u.transcript.entries)
	u.screenAddForTurn(kind, text, turnKey, turnID)
	if len(u.transcript.entries) > before {
		u.moveReplayEntry(len(u.transcript.entries)-1, turnKey, turnID)
	}
}

func (u *ui) screenReplayAddToolActivityStatus(text, turnKey, turnID string) {
	text = strings.TrimRight(sanitizeText(text), "\n")
	if strings.TrimSpace(text) == "" {
		return
	}
	turnKey = u.replayTurnKey(turnKey, turnID)
	for _, entry := range u.transcript.entries {
		if entry.turnKey == turnKey && entry.turnID == turnID && entry.kind == screenStatus && entry.toolActivity && entry.text == text {
			return
		}
	}
	before := len(u.transcript.entries)
	u.screenAddToolActivityStatus(text, turnKey, turnID)
	if len(u.transcript.entries) > before {
		u.moveReplayEntry(len(u.transcript.entries)-1, turnKey, turnID)
	}
}

func (u *ui) replayTurnKey(turnKey, turnID string) string {
	if turnID == "" {
		return turnKey
	}
	for _, entry := range u.transcript.entries {
		if entry.turnID == turnID && entry.turnKey != "" {
			return entry.turnKey
		}
	}
	return turnKey
}

func (u *ui) moveReplayEntry(index int, turnKey, turnID string) {
	if index < 0 || index >= len(u.transcript.entries) {
		return
	}
	insertAt := u.replayInsertionIndex(index, turnKey, turnID)
	if insertAt >= index {
		return
	}
	entry := u.transcript.entries[index]
	if entry.kind == screenTool || entry.toolActivity {
		if group, boundary, ok := u.replayToolGroupBefore(insertAt, turnKey, turnID); ok {
			entry.toolGroup = group
			entry.toolBoundary = boundary
		}
	}
	copy(u.transcript.entries[insertAt+1:index+1], u.transcript.entries[insertAt:index])
	u.transcript.entries[insertAt] = entry
	u.transcript.resequence()
	u.syncScreenProjection()
}

func (u *ui) replayInsertionIndex(lastIndex int, turnKey, turnID string) int {
	lastTurnIndex := -1
	for index := 0; index < lastIndex; index++ {
		entry := u.transcript.entries[index]
		if sameReplayTurn(entry, turnKey, turnID) {
			lastTurnIndex = index
		}
	}
	if lastTurnIndex < 0 {
		return lastIndex
	}
	return lastTurnIndex + 1
}

func (u *ui) replayToolGroupBefore(index int, turnKey, turnID string) (string, uint64, bool) {
	for index--; index >= 0; index-- {
		entry := u.transcript.entries[index]
		if !sameReplayTurn(entry, turnKey, turnID) {
			continue
		}
		if (entry.kind == screenTool || entry.toolActivity) && entry.toolGroup != "" {
			return entry.toolGroup, entry.toolBoundary, true
		}
	}
	return "", 0, false
}

func sameReplayTurn(entry sessionEntry, turnKey, turnID string) bool {
	if turnID != "" {
		return entry.turnID == turnID
	}
	return entry.turnKey == turnKey
}

func toolCompletionStatus(status string, exitCode any) string {
	status = strings.TrimSpace(status)
	exit := numberString(exitCode)
	if (status == "completed" || status == "success" || status == "succeeded") && (exit == "" || exit == "0") {
		return ""
	}
	if exit != "" {
		if status == "" {
			return "exit " + exit
		}
		return fmt.Sprintf("%s exit %s", status, exit)
	}
	return status
}

func screenPrefix(kind screenBlockKind) string {
	switch kind {
	case screenUser:
		return "› "
	case screenEvent:
		return "⟡ "
	case screenTool:
		return "↳ "
	case screenPlan:
		return "◇ "
	case screenWarning:
		return "⚠ "
	case screenError:
		return "× "
	case screenStatus:
		return "· "
	case screenCode:
		return "│ "
	case screenDiagram:
		return "▧ "
	default:
		return "  "
	}
}

func screenStyle(kind screenBlockKind) string {
	switch kind {
	case screenUser:
		return bold + cyan
	case screenAssistant:
		return green
	case screenTool:
		return yellow
	case screenPlan, screenInfo, screenDiagram:
		return cyan
	case screenCode:
		return muted
	case screenWarning:
		return yellow
	case screenError:
		return red
	case screenStatus, screenEvent:
		return muted
	default:
		return ""
	}
}

func screenStyledLine(kind screenBlockKind, value string, width int) string {
	value = truncateDisplay(value, width)
	padding := width - displayWidth(value)
	if padding < 0 {
		padding = 0
	}
	return screenStyle(kind) + value + reset + strings.Repeat(" ", padding)
}

func (u *ui) screenComposerLines(width int) []string {
	contentWidth := maxInt(1, width-4)
	label := " prompt "
	if u.replyTarget.key != "" {
		label = fmt.Sprintf(" ask turn %d ", u.replyTarget.ordinal)
	}
	topFill := width - 3 - displayWidth(label)
	if topFill < 0 {
		topFill = 0
	}
	lines := []string{"┌─" + label + strings.Repeat("─", topFill) + "┐"}
	if u.approval != nil {
		details := []string{
			"⚠ approval required",
			"action: " + u.approval.action,
			"cwd: " + displayCWD(u.approval.cwd),
		}
		if u.approval.reason != "" {
			details = append(details, "reason: "+u.approval.reason)
		}
		details = append(details, approvalHint(u.approval.available))
		for _, detail := range details {
			for index, line := range wrapDisplay(detail, contentWidth-2) {
				prefix := "  "
				if index == 0 && len(lines) == 1 {
					prefix = "⚠ "
				}
				lines = append(lines, screenComposerLine(prefix+line, width, screenWarning))
			}
		}
	} else if u.inputRequest != nil {
		details := u.inputQuestionDetails()
		for _, detail := range details {
			for index, line := range wrapDisplay(detail, contentWidth-2) {
				prefix := "  "
				if index == 0 && len(lines) == 1 {
					prefix = "? "
				}
				lines = append(lines, screenComposerLine(prefix+line, width, screenInfo))
			}
		}
	} else if u.dashboard {
		lines = append(lines, screenComposerLine("⌘ resume sessions  ·  ↑↓ select  ·  1–9 attach  ·  Esc close", width, screenInfo))
	} else {
		lines = append(lines, u.screenCommandPopupLines(width)...)
		value := string(u.input)
		if u.cursor < 0 {
			u.cursor = 0
		}
		if u.cursor > len(u.input) {
			u.cursor = len(u.input)
		}
		for index, line := range wrapDisplay(value, maxInt(1, contentWidth-2)) {
			prefix := "  "
			if index == 0 {
				prefix = "› "
			}
			lines = append(lines, screenComposerLine(prefix+line, width, screenUser))
		}
	}
	lines = append(lines, screenBottomBorder(width, ""))
	return lines
}

func screenComposerLine(value string, width int, kind screenBlockKind) string {
	contentWidth := maxInt(1, width-4)
	value = truncateDisplay(sanitizeText(value), contentWidth)
	return screenStyledLine(kind, "│ "+value+strings.Repeat(" ", contentWidth-displayWidth(value))+" │", width)
}

func screenBottomBorder(width int, status string) string {
	if strings.TrimSpace(status) == "" {
		return "└" + strings.Repeat("─", maxInt(0, width-2)) + "┘"
	}
	contentWidth := width - 3
	status = truncateDisplay(sanitizeText(status), maxInt(1, contentWidth-2))
	content := " " + status + " "
	dashes := contentWidth - displayWidth(content)
	if dashes < 0 {
		dashes = 0
	}
	left := dashes / 2
	return "└─" + strings.Repeat("─", left) + content + strings.Repeat("─", dashes-left) + "┘"
}

func (u *ui) screenStatusLabel() string {
	label := ""
	if u.connectionError != "" {
		label = "reconnecting"
		if u.reconnectAttempt > 0 {
			label += fmt.Sprintf(" · attempt %d", u.reconnectAttempt)
		}
		if u.reconnectRetryPending {
			label += " · retry pending"
		}
	} else if u.dashboard {
		label = "dashboard · sessions"
	} else if u.approval != nil {
		label = "approval · needs input"
	} else if u.inputRequest != nil {
		label = fmt.Sprintf("input %d/%d · needs input", u.inputRequest.index+1, len(u.inputRequest.questions))
	} else if u.busy {
		label = "working · Enter queue · Ctrl+Enter take over"
		if len(u.queuedPrompts) > 0 {
			label += fmt.Sprintf(" · %d queued", len(u.queuedPrompts))
		}
		if u.cancelAndSend != "" {
			label = "interrupting · cancel-and-send"
		}
	} else if u.replyTarget.key != "" {
		label = fmt.Sprintf("ask about turn %d · type follow-up", u.replyTarget.ordinal)
	} else if u.client == nil {
		label = "detached · app-server unavailable"
	} else {
		label = fmt.Sprintf("%s (%s) · %s", u.effectiveModel(), u.effectiveEffort(), u.effectiveApproval())
	}
	return statusBarBrand + " · " + label
}

func (u *ui) screenDockLine(width int) string {
	footer := "Enter send  ·  Alt+Enter newline  ·  Ctrl+U resume  ·  Ctrl-C twice quit  ·  Ctrl+X help"
	if u.connectionError != "" {
		footer = "retrying with bounded backoff  ·  prompt stays in composer  ·  Ctrl-C twice quit"
	} else if u.busy {
		footer = "Enter queue  ·  Ctrl+Enter cancel-and-send  ·  Ctrl-C interrupt, twice quit  ·  click edit"
	}
	if u.shortcuts {
		footer = "Ctrl+X close shortcuts  ·  Ctrl+\\ dashboard  ·  Esc close"
	}
	if u.dashboard {
		footer = "↑↓ select  ·  Enter/1–9 resume  ·  n new session  ·  r refresh  ·  Esc/Ctrl+\\ close"
	}
	if u.showToolOutput && !u.dashboard && !u.busy && !u.shortcuts && u.approval == nil && u.inputRequest == nil {
		footer = "Ctrl+O collapse tool use  ·  Ctrl+X shortcuts"
	}
	if u.approval != nil {
		footer = approvalHint(u.approval.available) + "  ·  Esc cancel"
	}
	if u.inputRequest != nil {
		footer = "↑↓ select  ·  Enter submit  ·  Ctrl-C interrupt  ·  Esc cancel"
	}
	if u.replyTarget.key != "" && u.approval == nil && u.inputRequest == nil && !u.busy {
		footer = fmt.Sprintf("turn %d selected · type follow-up  ·  Esc clear target", u.replyTarget.ordinal)
	}
	if u.scrollOffset > 0 && !u.shortcuts && u.approval == nil && u.inputRequest == nil {
		footer = "PageUp/PageDown or wheel scroll  ·  at older output"
	}
	if u.hoverText != "" && !u.shortcuts && u.approval == nil && u.inputRequest == nil && u.scrollOffset == 0 {
		footer = "hover · " + u.hoverText
	}
	if u.commandPopupVisible() && u.approval == nil && u.inputRequest == nil && !u.dashboard && !u.shortcuts {
		footer = "↑↓ choose  ·  Tab complete  ·  Enter run  ·  Esc close"
	}
	dock := u.screenStatusLabel()
	if footer != "" {
		dock += "  ·  " + footer
	}
	return screenStyledLine(screenEvent, dock, width)
}

func (u *ui) screenAdd(kind screenBlockKind, text string) {
	if kind == screenUser && u.activeScreenTurnKey == "" {
		u.beginScreenTurn("")
	}
	u.screenAddForTurn(kind, text, u.activeScreenTurnKey, u.activeScreenTurnID)
}

func (u *ui) screenAddForTurn(kind screenBlockKind, text, turnKey, turnID string) {
	text = strings.TrimRight(sanitizeText(text), "\n")
	if strings.TrimSpace(text) == "" {
		return
	}
	u.clearSelection()
	if kind == screenError {
		u.toolGroupBoundary++
	}
	entry := sessionEntry{
		role:         kind,
		kind:         kind,
		lifecycle:    "visible",
		text:         text,
		turnKey:      turnKey,
		turnID:       turnID,
		toolBoundary: u.toolGroupBoundary,
	}
	if kind == screenTool {
		entry.lifecycle = "active"
		entry.toolGroup = u.toolGroupForAppend(turnKey)
	}
	u.transcript.append(entry)
	u.syncScreenProjection()
}

func (u *ui) screenAddToolActivityStatus(text, turnKey, turnID string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	toolGroup := ""
	if len(u.transcript.entries) > 0 {
		previous := u.transcript.entries[len(u.transcript.entries)-1]
		if previous.turnKey == turnKey && previous.turnID == turnID && (previous.kind == screenTool || previous.toolActivity) {
			toolGroup = previous.toolGroup
		}
	}
	u.screenAddForTurn(screenStatus, text, turnKey, turnID)
	if len(u.transcript.entries) == 0 {
		return
	}
	last := &u.transcript.entries[len(u.transcript.entries)-1]
	if last.kind == screenStatus && last.turnKey == turnKey && last.turnID == turnID {
		last.toolActivity = true
		last.lifecycle = "completed"
		if toolGroup == "" {
			u.toolGroupSequence++
			toolGroup = fmt.Sprintf("tool-group-%d", u.toolGroupSequence)
		}
		last.toolGroup = toolGroup
		u.syncScreenProjection()
	}
}

func (u *ui) toolGroupForAppend(turnKey string) string {
	for index := len(u.transcript.entries) - 1; index >= 0; index-- {
		block := u.transcript.entries[index]
		if block.turnKey != turnKey {
			break
		}
		if block.toolBoundary != u.toolGroupBoundary {
			break
		}
		if (block.kind == screenTool || block.toolActivity) && block.toolGroup != "" {
			return block.toolGroup
		}
		break
	}
	u.toolGroupSequence++
	return fmt.Sprintf("tool-group-%d", u.toolGroupSequence)
}

func (u *ui) screenAppend(kind screenBlockKind, text string) {
	if text == "" {
		return
	}
	text = sanitizeText(text)
	u.clearSelection()
	turnKey, turnID := u.activeScreenTurnKey, u.activeScreenTurnID
	if len(u.transcript.entries) > 0 && u.transcript.entries[len(u.transcript.entries)-1].kind == kind &&
		u.transcript.entries[len(u.transcript.entries)-1].turnKey == turnKey {
		last := &u.transcript.entries[len(u.transcript.entries)-1]
		if kind == screenTool && last.text == "output" && !strings.HasPrefix(text, "\n") {
			last.text += "\n"
		}
		last.text += text
		u.syncScreenProjection()
		return
	}
	u.screenAddForTurn(kind, text, turnKey, turnID)
}

func (u *ui) newScreenTurnKey() string {
	u.screenTurnSequence++
	return fmt.Sprintf("screen-turn-%d", u.screenTurnSequence)
}

func (u *ui) beginScreenTurn(turnID string) {
	u.activeScreenTurnKey = u.newScreenTurnKey()
	u.activeScreenTurnID = turnID
}

func (u *ui) bindActiveScreenTurn(turnID string) {
	if u.activeScreenTurnKey == "" || turnID == "" {
		return
	}
	for index := range u.transcript.entries {
		if u.transcript.entries[index].turnKey == u.activeScreenTurnKey {
			u.transcript.entries[index].turnID = turnID
		}
	}
	u.syncScreenProjection()
	u.activeScreenTurnID = turnID
}

func (u *ui) endScreenTurn() {
	u.activeScreenTurnKey = ""
	u.activeScreenTurnID = ""
}

func gitBranch(cwd string) string {
	output, err := exec.Command("git", "-C", cwd, "branch", "--show-current").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func displayWidth(value string) int {
	width := 0
	for _, character := range value {
		width += runeDisplayWidth(character)
	}
	return width
}

func runeDisplayWidth(character rune) int {
	if character == '\t' {
		return 4
	}
	if character < 0x20 || character == 0x7f {
		return 0
	}
	if character >= 0x1100 && (character <= 0x115f || character == 0x2329 || character == 0x232a ||
		(character >= 0x2e80 && character <= 0xa4cf) || (character >= 0xac00 && character <= 0xd7a3) ||
		(character >= 0xf900 && character <= 0xfaff) || (character >= 0xfe10 && character <= 0xfe19) ||
		(character >= 0xfe30 && character <= 0xfe6f) || (character >= 0xff00 && character <= 0xff60) ||
		(character >= 0xffe0 && character <= 0xffe6)) {
		return 2
	}
	return 1
}

func truncateDisplay(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if displayWidth(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	remaining := width - 1
	var builder strings.Builder
	for _, character := range value {
		characterWidth := runeDisplayWidth(character)
		if characterWidth > remaining {
			break
		}
		builder.WriteRune(character)
		remaining -= characterWidth
	}
	builder.WriteRune('…')
	return builder.String()
}

func wrapDisplay(value string, width int) []string {
	if width < 1 {
		width = 1
	}
	value = sanitizeText(value)
	paragraphs := strings.Split(value, "\n")
	lines := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if paragraph == "" {
			lines = append(lines, "")
			continue
		}
		current := make([]rune, 0, len(paragraph))
		currentWidth := 0
		for _, character := range paragraph {
			characterWidth := runeDisplayWidth(character)
			if len(current) > 0 && currentWidth+characterWidth > width {
				lines = append(lines, string(current))
				current = current[:0]
				currentWidth = 0
			}
			current = append(current, character)
			currentWidth += characterWidth
		}
		lines = append(lines, string(current))
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
