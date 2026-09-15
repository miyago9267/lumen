package main

import "strings"

type inputVisualLine struct {
	start int
	end   int
}

func (u *ui) rememberPrompt(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if len(u.inputHistory) == 0 || u.inputHistory[len(u.inputHistory)-1] != value {
		u.inputHistory = append(u.inputHistory, value)
	}
	u.historyIndex = -1
	u.savedInput = nil
}

func (u *ui) markInputEdited() {
	if u.historyIndex >= 0 {
		u.historyIndex = -1
		u.savedInput = nil
	}
}

func (u *ui) movePromptUp() {
	if u.movePromptVisual(-1) {
		return
	}
	u.historyPrevious()
}

func (u *ui) movePromptDown() {
	if u.movePromptVisual(1) {
		return
	}
	u.historyNext()
}

func (u *ui) movePromptVisual(direction int) bool {
	lines := inputVisualLines(u.input, u.cursor, u.promptInputWidth())
	lineIndex := inputVisualLineIndex(u.input, u.cursor, lines)
	target := lineIndex + direction
	if target < 0 || target >= len(lines) {
		return false
	}
	current := lines[lineIndex]
	column := displayWidth(string(u.input[current.start:minInt(u.cursor, current.end)]))
	u.cursor = inputCursorForColumn(u.input, lines[target], column)
	return true
}

func (u *ui) promptInputWidth() int {
	width, _ := terminalSize()
	return maxInt(1, width-6)
}

func (u *ui) historyPrevious() {
	if len(u.inputHistory) == 0 {
		return
	}
	if u.historyIndex < 0 {
		u.savedInput = append([]rune(nil), u.input...)
		u.historyIndex = len(u.inputHistory)
	}
	if u.historyIndex > 0 {
		u.historyIndex--
	}
	u.input = []rune(u.inputHistory[u.historyIndex])
	u.cursor = len(u.input)
}

func (u *ui) historyNext() {
	if u.historyIndex < 0 {
		return
	}
	if u.historyIndex < len(u.inputHistory)-1 {
		u.historyIndex++
		u.input = []rune(u.inputHistory[u.historyIndex])
		u.cursor = len(u.input)
		return
	}
	u.historyIndex = -1
	u.input = append([]rune(nil), u.savedInput...)
	u.savedInput = nil
	u.cursor = len(u.input)
}

func inputVisualLines(value []rune, cursor, width int) []inputVisualLine {
	if width < 1 {
		width = 1
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(value) {
		cursor = len(value)
	}

	lines := make([]inputVisualLine, 0, len(value)/width+1)
	rowStart := 0
	rowWidth := 0
	for position, character := range value {
		if character == '\n' {
			lines = append(lines, inputVisualLine{start: rowStart, end: position})
			rowStart = position + 1
			rowWidth = 0
			continue
		}
		characterWidth := runeDisplayWidth(character)
		if position > rowStart && rowWidth+characterWidth > width {
			lines = append(lines, inputVisualLine{start: rowStart, end: position})
			rowStart = position
			rowWidth = 0
		}
		rowWidth += characterWidth
	}
	lines = append(lines, inputVisualLine{start: rowStart, end: len(value)})
	if len(lines) == 0 {
		lines = append(lines, inputVisualLine{})
	}
	return lines
}

func inputVisualLineIndex(value []rune, cursor int, lines []inputVisualLine) int {
	if len(lines) == 0 {
		return 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(value) {
		cursor = len(value)
	}
	for index, line := range lines {
		if cursor < line.end {
			return index
		}
		if cursor == line.end {
			if index == len(lines)-1 || (line.end < len(value) && value[line.end] == '\n') {
				return index
			}
			return index + 1
		}
	}
	return len(lines) - 1
}

func inputCursorForColumn(value []rune, line inputVisualLine, target int) int {
	if target <= 0 || line.start == line.end {
		return line.start
	}
	column := 0
	for position := line.start; position < line.end; position++ {
		characterWidth := runeDisplayWidth(value[position])
		if target < column+(characterWidth+1)/2 {
			return position
		}
		column += characterWidth
	}
	return line.end
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func (u *ui) placeCursorFromMouse(x, y, width, height int) bool {
	if !u.screenMode || u.approval != nil || u.dashboard {
		return false
	}
	composer := u.screenComposerLines(width)
	maxComposerLines := height - 4
	if maxComposerLines < 2 {
		maxComposerLines = 2
	}
	composer = trimComposerLines(composer, maxComposerLines)
	composerStartRow := height - len(composer)
	inputRow := y - composerStartRow - 1
	if inputRow < 0 {
		return false
	}
	inputWidth := maxInt(1, width-6)
	lines := inputVisualLines(u.input, u.cursor, inputWidth)
	if inputRow >= len(lines) {
		return false
	}
	column := x - 5
	if column < 0 {
		column = 0
	}
	u.cursor = inputCursorForColumn(u.input, lines[inputRow], column)
	return true
}
