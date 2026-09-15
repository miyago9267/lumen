package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
	kind screenBlockKind
	text string
}

const maxScreenBlocks = 256

func (u *ui) runFullscreen(initialPrompt string) error {
	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("enable raw terminal: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), state)

	u.screenMode = true
	u.branch = gitBranch(u.cwd)
	fmt.Fprint(u.out, "\x1b[?1049h\x1b[?25l\x1b[>1u\x1b[?1003h\x1b[?1006h")
	defer fmt.Fprint(u.out, "\x1b[?1006l\x1b[?1003l\x1b[<u\x1b[?25h\x1b[?1049l\r\n")

	u.screenAdd(screenEvent, "session_start  [app-server]")
	u.renderHistory()
	keyEvents := make(chan keyEvent, 8)
	go readKeys(u.in, keyEvents)
	renderTicker := time.NewTicker(33 * time.Millisecond)
	defer renderTicker.Stop()

	if initialPrompt != "" {
		if err := u.submit(initialPrompt); err != nil {
			return err
		}
	}
	u.renderScreen()
	dirty := false

	for {
		select {
		case key, ok := <-keyEvents:
			if !ok {
				return nil
			}
			if err := u.handleKey(key); errors.Is(err, errQuit) {
				return nil
			} else if err != nil {
				u.printError(err)
			}
			dirty = true
		case request := <-u.client.requests:
			u.handleServerRequest(request)
			dirty = true
		case notification := <-u.client.notifications:
			u.handleNotification(notification)
			dirty = true
		case err := <-u.client.errors:
			if err != nil {
				u.printError(err)
				u.renderScreen()
				return err
			}
		case <-u.client.closed:
			return errors.New("app-server closed")
		case <-renderTicker.C:
			if dirty {
				u.renderScreen()
				dirty = false
			}
		}
	}
}

func (u *ui) renderScreen() {
	width, height := terminalSize()
	fmt.Fprint(u.out, "\x1b[H", u.screenFrame(width, height))
}

func terminalSize() (int, int) {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width < 20 || height < 8 {
		return 100, 24
	}
	return width, height
}

func (u *ui) screenFrame(width, height int) string {
	if width < 20 {
		width = 20
	}
	if height < 8 {
		height = 8
	}

	composer := u.screenComposerLines(width)
	maxComposerLines := height - 4
	if maxComposerLines < 2 {
		maxComposerLines = 2
	}
	composer = trimComposerLines(composer, maxComposerLines)
	bodyRows := height - 3 - len(composer)
	if bodyRows < 1 {
		bodyRows = 1
	}

	transcriptRows := u.screenTranscriptRows(width)
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
	transcript := make([]string, 0, end-start)
	for _, row := range transcriptRows[start:end] {
		transcript = append(transcript, row.rendered)
	}
	for len(transcript) < bodyRows {
		transcript = append(transcript, "")
	}

	lines := []string{
		u.screenHeaderLine(width),
		screenStyledLine(screenEvent, strings.Repeat("┈", width), width),
	}
	lines = append(lines, transcript...)
	lines = append(lines, composer...)
	lines = append(lines, u.screenFooterLine(width))

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
	right := fmt.Sprintf("%s · %s", u.effectiveModel(), u.effectiveEffort())
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

func isToolOutputBlock(block screenBlock) bool {
	return block.kind == screenTool && (block.text == "output" || strings.HasPrefix(block.text, "output\n"))
}

func (u *ui) renderScreenHistoryItem(item historyItem) {
	switch item.Type {
	case "userMessage":
		if text := historyText(item.Content); text != "" {
			u.screenAdd(screenUser, text)
		}
	case "agentMessage":
		if item.Text != "" {
			u.screenAdd(screenAssistant, item.Text)
		}
	case "commandExecution":
		if item.Command != "" {
			u.screenAdd(screenTool, "$ "+item.Command)
		}
		if item.AggregatedOutput != "" {
			u.screenAdd(screenTool, "output\n"+item.AggregatedOutput)
		}
		if item.Status != "" {
			status := item.Status
			if exitCode := numberString(item.ExitCode); exitCode != "" {
				status = fmt.Sprintf("%s exit %s", status, exitCode)
			}
			u.screenAdd(screenStatus, status)
		}
	case "fileChange":
		if item.Status != "" {
			u.screenAdd(screenStatus, "file change "+item.Status)
		}
	case "mcpToolCall":
		u.screenAdd(screenTool, fmt.Sprintf("◇ %s/%s", item.Server, item.Tool))
	case "plan":
		if item.Text != "" {
			u.screenAdd(screenPlan, item.Text)
		}
	case "reasoning":
		if len(item.Summary) > 0 {
			u.screenAdd(screenEvent, "reasoning: "+strings.Join(item.Summary, " "))
		}
	}
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
		question := u.currentInputQuestion()
		details := []string{
			fmt.Sprintf("? question %d/%d", u.inputRequest.index+1, len(u.inputRequest.questions)),
			question.Question,
			"answer: " + u.inputValueWithCursor(),
		}
		if question.Header != "" {
			details[0] += " · " + question.Header
		}
		if len(question.Options) > 0 {
			options := make([]string, 0, len(question.Options))
			for index, option := range question.Options {
				options = append(options, fmt.Sprintf("%d) %s", index+1, option.Label))
			}
			details = append(details, "options: "+strings.Join(options, "  "))
		}
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
		lines = append(lines, screenComposerLine("⌘ dashboard  ·  ↑↓ select  ·  1–9 attach  ·  Esc close", width, screenInfo))
	} else {
		value := append([]rune(nil), u.input...)
		if u.cursor < 0 {
			u.cursor = 0
		}
		if u.cursor > len(value) {
			u.cursor = len(value)
		}
		value = append(value, 0)
		copy(value[u.cursor+1:], value[u.cursor:])
		value[u.cursor] = cursor
		for index, line := range wrapDisplay(string(value), maxInt(1, contentWidth-2)) {
			prefix := "  "
			if index == 0 {
				prefix = "› "
			}
			lines = append(lines, screenComposerLine(prefix+line, width, screenUser))
		}
	}
	lines = append(lines, screenBottomBorder(width, u.screenStatusLabel()))
	return lines
}

func screenComposerLine(value string, width int, kind screenBlockKind) string {
	contentWidth := maxInt(1, width-4)
	value = truncateDisplay(sanitizeText(value), contentWidth)
	return screenStyledLine(kind, "│ "+value+strings.Repeat(" ", contentWidth-displayWidth(value))+" │", width)
}

func screenBottomBorder(width int, status string) string {
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
	if u.dashboard {
		return "dashboard · sessions"
	}
	if u.approval != nil {
		return "approval · " + approvalHint(u.approval.available)
	}
	if u.inputRequest != nil {
		return fmt.Sprintf("input %d/%d · Enter next · Esc cancel", u.inputRequest.index+1, len(u.inputRequest.questions))
	}
	if u.busy {
		label := "working · Enter queue · Ctrl+Enter take over"
		if len(u.queuedPrompts) > 0 {
			label += fmt.Sprintf(" · %d queued", len(u.queuedPrompts))
		}
		if u.cancelAndSend != "" {
			label = "interrupting · cancel-and-send"
		}
		return label
	}
	return fmt.Sprintf("%s (%s) · %s", u.effectiveModel(), u.effectiveEffort(), u.effectiveApproval())
}

func (u *ui) screenFooterLine(width int) string {
	footer := "Enter send  ·  Alt+Enter newline  ·  ↑↓ history  ·  click edit  ·  Ctrl+O tools  ·  Ctrl+X help"
	if u.busy {
		footer = "Enter queue  ·  Ctrl+Enter cancel-and-send  ·  Ctrl-C interrupt  ·  click edit"
	}
	if u.shortcuts {
		footer = "Ctrl+X close shortcuts  ·  Ctrl+\\ dashboard  ·  Esc close"
	}
	if u.dashboard {
		footer = "↑↓ select  ·  Enter/1–9 attach  ·  n new session  ·  r refresh  ·  Ctrl+\\ close"
	}
	if u.showToolOutput && !u.dashboard && !u.busy && !u.shortcuts && u.approval == nil && u.inputRequest == nil {
		footer = "Ctrl+O collapse tool output  ·  Ctrl+X shortcuts"
	}
	if u.approval != nil {
		footer = approvalHint(u.approval.available) + "  ·  Esc cancel"
	}
	if u.inputRequest != nil {
		footer = "Enter next  ·  Ctrl-C interrupt  ·  Esc cancel"
	}
	if u.scrollOffset > 0 && !u.shortcuts && u.approval == nil && u.inputRequest == nil {
		footer = "PageUp/PageDown or wheel scroll  ·  at older output"
	}
	if u.hoverText != "" && !u.shortcuts && u.approval == nil && u.inputRequest == nil && u.scrollOffset == 0 {
		footer = "hover · " + u.hoverText
	}
	return screenStyledLine(screenEvent, footer, width)
}

func (u *ui) screenAdd(kind screenBlockKind, text string) {
	if !u.screenMode {
		return
	}
	text = strings.TrimRight(sanitizeText(text), "\n")
	if strings.TrimSpace(text) == "" {
		return
	}
	u.screenBlocks = append(u.screenBlocks, screenBlock{kind: kind, text: text})
	u.scrollOffset = 0
	if len(u.screenBlocks) > maxScreenBlocks {
		u.screenBlocks = u.screenBlocks[len(u.screenBlocks)-maxScreenBlocks:]
	}
}

func (u *ui) screenAppend(kind screenBlockKind, text string) {
	if !u.screenMode || text == "" {
		return
	}
	text = sanitizeText(text)
	if len(u.screenBlocks) > 0 && u.screenBlocks[len(u.screenBlocks)-1].kind == kind {
		if kind == screenTool && u.screenBlocks[len(u.screenBlocks)-1].text == "output" && !strings.HasPrefix(text, "\n") {
			u.screenBlocks[len(u.screenBlocks)-1].text += "\n"
		}
		u.screenBlocks[len(u.screenBlocks)-1].text += text
		u.scrollOffset = 0
		return
	}
	u.screenAdd(kind, text)
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
