package lumen

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type screenTurnAction uint8

const (
	screenTurnActionNone screenTurnAction = iota
	screenTurnActionAsk
	screenTurnActionFork
)

type screenActionRange struct {
	action screenTurnAction
	start  int
	end    int
}

type screenTurnTarget struct {
	key     string
	id      string
	ordinal int
}

type screenTranscriptRow struct {
	rendered    string
	tooltip     string
	copyText    string
	copyStart   int
	copyable    bool
	turnKey     string
	turnID      string
	turnOrdinal int
	toolGroup   string
	actions     []screenActionRange
}

type screenContentLine struct {
	kind screenBlockKind
	text string
}

func (u *ui) screenTranscriptRows(width int) []screenTranscriptRow {
	if u.dashboard {
		return u.screenDashboardRows(width)
	}
	if u.shortcuts {
		return screenRowsForBlock(screenBlock{
			kind: screenInfo,
			text: "Enter send · Alt+Enter newline\n↑↓ history · Ctrl+U resume\nclick composer/turn · [fork] branch\nCtrl-C twice quit\nPageUp/PageDown or wheel scroll\nCtrl+O tool output · /dashboard sessions\n/help commands",
		}, width)
	}
	if u.screenShowsWelcome() {
		return u.screenWelcomeRows(width)
	}
	blocks := u.projectedScreenBlocks()
	rows := make([]screenTranscriptRow, 0, len(blocks)+4)
	turnOrdinal := 0
	for index := 0; index < len(blocks); {
		block := blocks[index]
		if block.turnKey != "" {
			start := index
			for index < len(blocks) && blocks[index].turnKey == block.turnKey {
				index++
			}
			turnOrdinal++
			rows = append(rows, u.screenTurnRows(blocks[start:index], width, turnOrdinal)...)
			continue
		}
		if isToolActivity(block) && !u.toolGroupExpanded(block) {
			start := index
			for index+1 < len(blocks) && sameToolGroup(block, blocks[index+1]) {
				index++
			}
			rows = append(rows, u.collapsedToolGroupRows(block, index-start+1, width, "", "", 0)...)
			index++
			continue
		}
		if block.kind == screenTool {
			rows = append(rows, screenToolRows(block, width)...)
			index++
			continue
		}
		rows = append(rows, screenRowsForBlock(block, width)...)
		index++
	}
	if u.reasoningActive {
		rows = append(rows, u.screenReasoningRow(width)...)
	}
	return rows
}

func (u *ui) screenTurnRows(blocks []screenBlock, width, ordinal int) []screenTranscriptRow {
	if len(blocks) == 0 {
		return nil
	}
	key := blocks[0].turnKey
	id := ""
	for _, block := range blocks {
		if block.turnID != "" {
			id = block.turnID
			break
		}
	}
	title := fmt.Sprintf("turn %d", ordinal)
	if u.replyTarget.key == key {
		title += " · selected"
	}
	rows := []screenTranscriptRow{{
		rendered:    screenCardBorder("┌─ "+title+" ", "┐", width),
		turnKey:     key,
		turnID:      id,
		turnOrdinal: ordinal,
	}}
	for index := 0; index < len(blocks); {
		block := blocks[index]
		if isToolActivity(block) && !u.toolGroupExpanded(block) {
			start := index
			for index+1 < len(blocks) && sameToolGroup(block, blocks[index+1]) {
				index++
			}
			rows = append(rows, u.collapsedToolGroupRows(block, index-start+1, width, key, id, ordinal)...)
			index++
			continue
		}
		rows = append(rows, screenTurnBlockRows(block, width, key, id, ordinal)...)
		index++
	}
	if u.reasoningActive && key == u.activeScreenTurnKey {
		dots := int((u.reasoningFrame / 6) % 4)
		rows = append(rows, screenTurnBlockRows(screenBlock{
			kind:    screenStatus,
			text:    "⟡ thinking" + strings.Repeat(".", dots),
			turnKey: key,
			turnID:  id,
		}, width, key, id, ordinal)...)
	}
	rows = append(rows, screenTurnActionRow(key, id, ordinal, width))
	rows = append(rows, screenTranscriptRow{
		rendered:    screenCardBorder("└", "┘", width),
		tooltip:     screenTurnTooltip(ordinal, id != ""),
		turnKey:     key,
		turnID:      id,
		turnOrdinal: ordinal,
	})
	return rows
}

func isToolActivity(block screenBlock) bool {
	return block.kind == screenTool || block.toolActivity
}

func sameToolGroup(left, right screenBlock) bool {
	if !isToolActivity(left) || !isToolActivity(right) {
		return false
	}
	return left.toolGroup == right.toolGroup
}

func (u *ui) toolGroupExpanded(block screenBlock) bool {
	if u.showToolOutput {
		return true
	}
	return block.toolGroup != "" && u.expandedToolGroups[block.toolGroup]
}

func collapsedToolLabelForCount(count int) string {
	if count <= 1 {
		return "tools hidden · click to show · Ctrl+O to show"
	}
	return fmt.Sprintf("tools hidden · %d uses · click to show · Ctrl+O to show", count)
}

func (u *ui) collapsedToolGroupRows(block screenBlock, count, width int, key, id string, ordinal int) []screenTranscriptRow {
	collapsed := screenBlock{
		kind:         screenStatus,
		text:         collapsedToolLabelForCount(count),
		turnKey:      key,
		turnID:       id,
		toolActivity: true,
		toolGroup:    block.toolGroup,
	}
	rows := screenBlockRows(collapsed, width, key, id, ordinal)
	tooltip := fmt.Sprintf("tool group · %d use", count)
	if count != 1 {
		tooltip += "s"
	}
	tooltip += " · click to show detail"
	for index := range rows {
		rows[index].tooltip = tooltip
		rows[index].copyable = false
		rows[index].copyText = ""
	}
	return rows
}

func (u *ui) toggleToolGroup(group string) {
	if group == "" {
		return
	}
	if u.expandedToolGroups == nil {
		u.expandedToolGroups = make(map[string]bool)
	}
	u.expandedToolGroups[group] = !u.expandedToolGroups[group]
	u.clearSelection()
	u.hoverText = ""
	u.hoverRow = -1
}

func screenTurnBlockRows(block screenBlock, width int, key, id string, ordinal int) []screenTranscriptRow {
	return screenBlockRows(block, width, key, id, ordinal)
}

func screenBlockRows(block screenBlock, width int, key, id string, ordinal int) []screenTranscriptRow {
	content := screenContentLines(block)
	if len(content) == 0 {
		return nil
	}
	tooltip := screenTurnBlockTooltip(block, ordinal, id != "")
	if key == "" {
		tooltip = screenBlockTooltip(block)
	}
	rows := make([]screenTranscriptRow, 0, len(content))
	basePrefix := screenBlockInlinePrefix(block)
	for lineIndex, line := range content {
		prefix := basePrefix
		if lineIndex > 0 {
			prefix = strings.Repeat(" ", displayWidth(basePrefix))
			if line.kind != block.kind {
				prefix += screenPrefix(line.kind)
			}
		}
		wrapped := wrapDisplay(line.text, maxInt(1, width-displayWidth(prefix)))
		for wrapIndex, value := range wrapped {
			copyText := value
			if wrapIndex > 0 {
				value = strings.Repeat(" ", displayWidth(prefix)) + value
			} else {
				value = prefix + value
			}
			rows = append(rows, screenTranscriptRow{
				rendered:    screenStyledLine(line.kind, value, width),
				tooltip:     tooltip,
				copyText:    copyText,
				copyStart:   displayWidth(prefix),
				copyable:    true,
				turnKey:     key,
				turnID:      id,
				turnOrdinal: ordinal,
				toolGroup:   block.toolGroup,
			})
		}
	}
	return rows
}

func screenBlockInlinePrefix(block screenBlock) string {
	label := screenBlockLabel(block)
	if block.kind == screenUser || block.kind == screenAssistant {
		return label + " › "
	}
	symbol := "·"
	switch block.kind {
	case screenEvent:
		symbol = "⟡"
	case screenTool:
		symbol = "↳"
	case screenPlan:
		symbol = "◇"
	case screenWarning:
		symbol = "⚠"
	case screenError:
		symbol = "×"
	}
	return label + " " + symbol + " "
}

func screenTurnBlockTooltip(block screenBlock, ordinal int, canFork bool) string {
	preview := strings.TrimSpace(strings.SplitN(block.text, "\n", 2)[0])
	if preview == "" {
		return screenTurnTooltip(ordinal, canFork)
	}
	return fmt.Sprintf("turn %d · %s · %s", ordinal, screenBlockLabel(block), truncateDisplay(preview, 72))
}

func screenTurnActionRow(key, id string, ordinal, width int) screenTranscriptRow {
	const askLabel = "[ask]"
	const forkLabel = "[fork]"
	value := askLabel
	actions := make([]screenActionRange, 0, 2)
	if action, ok := screenActionRangeFor(screenTurnActionAsk, 2, askLabel, width); ok {
		actions = append(actions, action)
	}
	if id != "" {
		value += "  " + forkLabel
		forkStart := 2 + displayWidth(askLabel+"  ")
		if action, ok := screenActionRangeFor(screenTurnActionFork, forkStart, forkLabel, width); ok {
			actions = append(actions, action)
		}
	}
	return screenTranscriptRow{
		rendered:    screenCardContent(value, screenInfo, width),
		tooltip:     screenTurnTooltip(ordinal, id != ""),
		turnKey:     key,
		turnID:      id,
		turnOrdinal: ordinal,
		actions:     actions,
	}
}

func screenActionRangeFor(action screenTurnAction, start int, label string, width int) (screenActionRange, bool) {
	maxColumn := width - 3
	if start > maxColumn {
		return screenActionRange{}, false
	}
	end := start + displayWidth(label) - 1
	if end > maxColumn {
		end = maxColumn
	}
	return screenActionRange{action: action, start: start, end: end}, true
}

func screenTurnTooltip(ordinal int, canFork bool) string {
	tooltip := fmt.Sprintf("turn %d · click to ask", ordinal)
	if canFork {
		tooltip += " · [fork] branch from here"
	}
	return tooltip
}

func (u *ui) screenShowsWelcome() bool {
	return u.screenMode && u.showWelcome && !u.busy && u.approval == nil && u.inputRequest == nil
}

func (u *ui) screenWelcomeRows(width int) []screenTranscriptRow {
	tooltip := "welcome · new thread"
	return []screenTranscriptRow{
		centeredScreenRow(screenInfo, "Lumen", tooltip, width),
		centeredScreenRow(screenStatus, "ready for a new thread", tooltip, width),
		centeredScreenRow(screenStatus, "Ask about this repository, inspect files, or start a change.", tooltip, width),
		{rendered: screenStyledLine(screenEvent, "", width), tooltip: tooltip},
		{rendered: screenCardBorder("┌─ try a prompt ", "┐", width), tooltip: tooltip},
		{rendered: screenCardContent("› inspect the current repo", screenUser, width), tooltip: tooltip, copyText: "inspect the current repo", copyStart: 4, copyable: true},
		{rendered: screenCardContent("› explain this project structure", screenUser, width), tooltip: tooltip, copyText: "explain this project structure", copyStart: 4, copyable: true},
		{rendered: screenCardBorder("└", "┘", width), tooltip: tooltip},
		centeredScreenRow(screenStatus, "Ctrl+X shortcuts · Ctrl+\\ sessions · /help", tooltip, width),
	}
}

func (u *ui) screenReasoningRow(width int) []screenTranscriptRow {
	dots := int((u.reasoningFrame / 6) % 4)
	return screenRowsForBlock(screenBlock{
		kind: screenStatus,
		text: "⟡ thinking" + strings.Repeat(".", dots),
	}, width)
}

func screenCenteredLine(kind screenBlockKind, value string, width int) string {
	value = truncateDisplay(sanitizeText(value), width)
	leftPadding := (width - displayWidth(value)) / 2
	if leftPadding < 0 {
		leftPadding = 0
	}
	return screenStyledLine(kind, strings.Repeat(" ", leftPadding)+value, width)
}

func centeredCopyStart(value string, width int) int {
	value = truncateDisplay(sanitizeText(value), width)
	return maxInt(0, (width-displayWidth(value))/2)
}

func centeredScreenRow(kind screenBlockKind, value, tooltip string, width int) screenTranscriptRow {
	value = truncateDisplay(sanitizeText(value), width)
	return screenTranscriptRow{
		rendered:  screenCenteredLine(kind, value, width),
		tooltip:   tooltip,
		copyText:  value,
		copyStart: centeredCopyStart(value, width),
		copyable:  true,
	}
}

func screenRowsForBlock(block screenBlock, width int) []screenTranscriptRow {
	return screenBlockRows(block, width, "", "", 0)
}

func screenBlockLines(block screenBlock, width int) []string {
	rows := screenRowsForBlock(block, width)
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, row.rendered)
	}
	return lines
}

func screenToolRows(block screenBlock, width int) []screenTranscriptRow {
	return screenBlockRows(block, width, "", "", 0)
}

func screenBlockLabel(block screenBlock) string {
	if block.kind == screenTool && (block.text == "output" || strings.HasPrefix(block.text, "output\n")) {
		return "tool output"
	}
	switch block.kind {
	case screenUser:
		return "you"
	case screenAssistant:
		return "assistant"
	case screenTool:
		return "tool"
	case screenPlan:
		return "plan"
	case screenStatus:
		return "status"
	case screenInfo:
		if strings.HasPrefix(block.text, "Enter send") {
			return "shortcuts"
		}
		return "info"
	case screenWarning:
		return "warning"
	case screenError:
		return "error"
	case screenCode:
		return "code"
	case screenDiagram:
		return "diagram"
	default:
		return "event"
	}
}

func screenCardBorder(left, right string, width int) string {
	valueWidth := displayWidth(left) + displayWidth(right)
	fill := width - valueWidth
	if fill < 0 {
		fill = 0
	}
	return screenStyledLine(screenEvent, left+strings.Repeat("─", fill)+right, width)
}

func screenCardContent(value string, kind screenBlockKind, width int) string {
	innerWidth := maxInt(1, width-4)
	value = truncateDisplay(value, innerWidth)
	padding := innerWidth - displayWidth(value)
	return screenStyledLine(kind, "│ "+value+strings.Repeat(" ", padding)+" │", width)
}

func screenContentLines(block screenBlock) []screenContentLine {
	if block.kind == screenAssistant {
		return markdownContentLines(block.text)
	}
	text := strings.TrimRight(block.text, "\n")
	if text == "" {
		return nil
	}
	parts := strings.Split(text, "\n")
	lines := make([]screenContentLine, 0, len(parts))
	for _, part := range parts {
		lines = append(lines, screenContentLine{kind: block.kind, text: part})
	}
	return lines
}

func screenBlockTooltip(block screenBlock) string {
	text := strings.TrimSpace(strings.SplitN(block.text, "\n", 2)[0])
	text = sanitizeText(text)
	if text == "" {
		return ""
	}
	label := screenBlockLabel(block)
	if label == "" {
		return ""
	}
	return label + " · " + truncateDisplay(text, 96)
}

func screenPlainLine(value string) string {
	var builder strings.Builder
	for index := 0; index < len(value); {
		if value[index] == '\x1b' {
			index++
			if index < len(value) && value[index] == '[' {
				index++
				for index < len(value) {
					character := value[index]
					index++
					if character >= 0x40 && character <= 0x7e {
						break
					}
				}
			}
			continue
		}
		character, size := utf8.DecodeRuneInString(value[index:])
		builder.WriteRune(character)
		index += size
	}
	return builder.String()
}

func displaySlice(value string, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end < start {
		return ""
	}
	var builder strings.Builder
	column := 0
	for _, character := range value {
		characterWidth := runeDisplayWidth(character)
		nextColumn := column + characterWidth
		if nextColumn > start && column < end {
			builder.WriteRune(character)
		}
		column = nextColumn
	}
	return builder.String()
}

func highlightScreenRange(rendered string, start, end int) string {
	plain := screenPlainLine(rendered)
	if start < 0 {
		start = 0
	}
	if end > displayWidth(plain) {
		end = displayWidth(plain)
	}
	if start >= end {
		return rendered
	}
	var builder strings.Builder
	column := 0
	for _, character := range plain {
		characterWidth := runeDisplayWidth(character)
		selected := column+characterWidth > start && column < end
		if selected {
			builder.WriteString("\x1b[7m")
		}
		builder.WriteRune(character)
		if selected {
			builder.WriteString("\x1b[27m")
		}
		column += characterWidth
	}
	return builder.String()
}

func normalizedScreenSelection(selection screenSelection) (screenPoint, screenPoint, bool) {
	if !selection.active {
		return screenPoint{}, screenPoint{}, false
	}
	start, end := selection.start, selection.end
	if start.row > end.row || (start.row == end.row && start.col > end.col) {
		start, end = end, start
	}
	return start, end, true
}

func (u *ui) screenRenderedRow(row screenTranscriptRow, rowIndex, width int) string {
	rendered := row.rendered
	if u.hoverText != "" && u.hoverRow == rowIndex {
		rendered = highlightScreenRange(rendered, 0, width)
	}
	start, end, ok := normalizedScreenSelection(u.selection)
	if !ok || rowIndex < start.row || rowIndex > end.row {
		return rendered
	}
	from, to := 0, width
	if rowIndex == start.row {
		from = start.col
	}
	if rowIndex == end.row {
		to = end.col + 1
	}
	if from < 0 {
		from = 0
	}
	if to > width {
		to = width
	}
	if from >= to {
		return rendered
	}
	return highlightScreenRange(rendered, from, to)
}

func (u *ui) selectedTranscriptText(width int) string {
	start, end, ok := normalizedScreenSelection(u.selection)
	if !ok {
		return ""
	}
	rows := u.screenTranscriptRows(width)
	if start.row < 0 {
		start.row = 0
	}
	if end.row >= len(rows) {
		end.row = len(rows) - 1
	}
	if len(rows) == 0 || start.row > end.row {
		return ""
	}
	parts := make([]string, 0, end.row-start.row+1)
	for rowIndex := start.row; rowIndex <= end.row; rowIndex++ {
		row := rows[rowIndex]
		if !row.copyable {
			continue
		}
		from, to := 0, displayWidth(row.copyText)
		if rowIndex == start.row {
			from = maxInt(0, start.col-row.copyStart)
		}
		if rowIndex == end.row {
			to = minInt(to, end.col+1-row.copyStart)
		}
		if from >= to {
			if rowIndex > start.row && rowIndex < end.row {
				parts = append(parts, "")
			}
			continue
		}
		parts = append(parts, displaySlice(row.copyText, from, to))
	}
	return strings.Join(parts, "\n")
}

func (u *ui) clearSelection() {
	u.selection = screenSelection{}
}

func (u *ui) screenTranscriptHit(x, y, width, height int) (screenPoint, bool) {
	if width < 20 {
		width = 20
	}
	if height < 8 {
		height = 8
	}
	_, bodyRows := u.screenComposerAndBodyRows(width, height)
	if bodyRows < 1 || y < 3 || y > bodyRows+2 {
		return screenPoint{}, false
	}
	rows := u.screenTranscriptRows(width)
	if len(rows) == 0 {
		return screenPoint{}, false
	}
	rowIndex := -1
	if u.screenShowsWelcome() {
		u.scrollOffset = 0
		if len(rows) > bodyRows {
			rows = rows[:bodyRows]
		}
		topPadding := (bodyRows - len(rows)) / 2
		rowIndex = y - 3 - topPadding
		if rowIndex < 0 || rowIndex >= len(rows) {
			return screenPoint{}, false
		}
	} else {
		u.scrollOffset = clampScreenScrollOffset(u.scrollOffset, len(rows), bodyRows)
		end := len(rows) - u.scrollOffset
		if end < 0 {
			end = 0
		}
		if end > len(rows) {
			end = len(rows)
		}
		start := maxInt(0, end-bodyRows)
		if start > end {
			start = end
		}
		rowIndex = start + y - 3
		if rowIndex < start || rowIndex >= end {
			return screenPoint{}, false
		}
	}
	column := x - 1
	if column < 0 {
		column = 0
	}
	if column >= width {
		column = width - 1
	}
	return screenPoint{row: rowIndex, col: column}, true
}

func (u *ui) screenTurnTargetAtPoint(point screenPoint, width int) (screenTurnTarget, screenTurnAction, bool) {
	rows := u.screenTranscriptRows(width)
	if point.row < 0 || point.row >= len(rows) || rows[point.row].turnKey == "" {
		return screenTurnTarget{}, screenTurnActionNone, false
	}
	row := rows[point.row]
	target := screenTurnTarget{key: row.turnKey, id: row.turnID, ordinal: row.turnOrdinal}
	for _, action := range row.actions {
		if point.col >= action.start && point.col <= action.end {
			return target, action.action, true
		}
	}
	return target, screenTurnActionNone, true
}

func (u *ui) beginScreenSelection(x, y, width, height int) bool {
	if !u.screenMode || u.dashboard || u.shortcuts || u.approval != nil || u.inputRequest != nil {
		return false
	}
	point, ok := u.screenTranscriptHit(x, y, width, height)
	if !ok {
		return false
	}
	u.selection = screenSelection{start: point, end: point, active: true, dragging: true}
	u.hoverText = ""
	return true
}

func (u *ui) updateScreenSelection(x, y, width, height int) bool {
	if !u.selection.active || !u.selection.dragging {
		return false
	}
	point, ok := u.screenTranscriptHit(x, y, width, height)
	if !ok {
		return false
	}
	u.selection.end = point
	return true
}

func (u *ui) finishScreenSelection(x, y, width, height int) error {
	if !u.selection.active {
		return nil
	}
	releasePoint, releaseOK := u.screenTranscriptHit(x, y, width, height)
	startPoint := u.selection.start
	u.updateScreenSelection(x, y, width, height)
	u.selection.dragging = false
	click := releaseOK && startPoint == releasePoint
	if click {
		rows := u.screenTranscriptRows(width)
		if releasePoint.row >= 0 && releasePoint.row < len(rows) && rows[releasePoint.row].toolGroup != "" {
			u.toggleToolGroup(rows[releasePoint.row].toolGroup)
			return nil
		}
		target, action, ok := u.screenTurnTargetAtPoint(releasePoint, width)
		u.clearSelection()
		if ok {
			if action == screenTurnActionNone {
				u.selectScreenTurnForAsk(target)
				return nil
			}
			return u.handleScreenTurnAction(action, target)
		}
	}
	text := u.selectedTranscriptText(width)
	u.clearSelection()
	if text == "" {
		return nil
	}
	writer := u.clipboard
	if writer == nil {
		writer = writeClipboard
	}
	if err := writer(text); err != nil {
		return fmt.Errorf("copy selection: %w", err)
	}
	u.printStatus(fmt.Sprintf("copied %d chars", utf8.RuneCountInString(text)), green)
	return nil
}

func (u *ui) selectScreenTurnForAsk(target screenTurnTarget) {
	u.replyTarget = target
	u.showWelcome = false
	u.hoverText = ""
	u.hoverRow = -1
}

func (u *ui) clearReplyTarget() {
	u.replyTarget = screenTurnTarget{}
}

func (u *ui) updateHover(x, y, width, height int) {
	u.hoverText = ""
	u.hoverRow = -1
	if !u.screenMode {
		return
	}
	point, ok := u.screenTranscriptHit(x, y, width, height)
	if !ok {
		return
	}
	transcriptRows := u.screenTranscriptRows(width)
	if point.row >= 0 && point.row < len(transcriptRows) {
		u.hoverText = transcriptRows[point.row].tooltip
		u.hoverRow = point.row
	}
}
