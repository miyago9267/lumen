package main

import (
	"fmt"
	"strings"
)

type screenTranscriptRow struct {
	rendered string
	tooltip  string
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
			text: "shortcuts\n  Enter send\n  Alt+Enter newline\n  ↑↓ history\n  click composer to edit\n  Ctrl-C interrupt/quit\n  PageUp/PageDown or wheel scroll\n  Ctrl+O expand/collapse tool output\n  /dashboard supervise sessions\n  /help commands",
		}, width)
	}

	rows := make([]screenTranscriptRow, 0, len(u.screenBlocks)+4)
	for index := 0; index < len(u.screenBlocks); index++ {
		block := u.screenBlocks[index]
		if !u.showToolOutput && isToolOutputBlock(block) {
			rows = append(rows, screenRowsForBlock(screenBlock{
				kind: screenStatus,
				text: "tool output hidden · Ctrl+O to expand",
			}, width)...)
			continue
		}
		if block.kind == screenUser {
			conversation := []screenBlock{block}
			for index+1 < len(u.screenBlocks) && u.screenBlocks[index+1].kind == screenAssistant {
				index++
				conversation = append(conversation, u.screenBlocks[index])
			}
			rows = append(rows, screenConversationRows(conversation, width)...)
			continue
		}
		if block.kind == screenTool {
			rows = append(rows, screenToolRows(block, width)...)
			continue
		}
		rows = append(rows, screenRowsForBlock(block, width)...)
	}
	return rows
}

func screenRowsForBlock(block screenBlock, width int) []screenTranscriptRow {
	content := screenContentLines(block)
	rows := make([]screenTranscriptRow, 0, len(content))
	tooltip := screenBlockTooltip(block)
	for _, line := range content {
		prefix := screenPrefix(line.kind)
		wrapped := wrapDisplay(line.text, maxInt(1, width-displayWidth(prefix)))
		for index, value := range wrapped {
			if index > 0 {
				value = strings.Repeat(" ", displayWidth(prefix)) + value
			} else {
				value = prefix + value
			}
			rows = append(rows, screenTranscriptRow{
				rendered: screenStyledLine(line.kind, value, width),
				tooltip:  tooltip,
			})
		}
	}
	return rows
}

func screenBlockLines(block screenBlock, width int) []string {
	rows := screenRowsForBlock(block, width)
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, row.rendered)
	}
	return lines
}

func screenConversationRows(blocks []screenBlock, width int) []screenTranscriptRow {
	if len(blocks) == 0 {
		return nil
	}
	label := fmt.Sprintf("conversation · %d messages", len(blocks))
	rows := []screenTranscriptRow{{
		rendered: screenCardBorder("┌─ "+label+" ", "┐", width),
		tooltip:  "conversation · hover a row for details",
	}}
	for _, block := range blocks {
		for _, line := range screenContentLines(block) {
			prefix := screenPrefix(line.kind)
			innerWidth := maxInt(1, width-4)
			wrapped := wrapDisplay(line.text, maxInt(1, innerWidth-displayWidth(prefix)))
			for index, value := range wrapped {
				if index > 0 {
					value = strings.Repeat(" ", displayWidth(prefix)) + value
				} else {
					value = prefix + value
				}
				rows = append(rows, screenTranscriptRow{
					rendered: screenCardContent(value, line.kind, width),
					tooltip:  screenBlockTooltip(block),
				})
			}
		}
	}
	rows = append(rows, screenTranscriptRow{
		rendered: screenCardBorder("└", "┘", width),
		tooltip:  "conversation · end",
	})
	return rows
}

func screenToolRows(block screenBlock, width int) []screenTranscriptRow {
	content := screenContentLines(block)
	if len(content) == 0 {
		return nil
	}
	label := "tool"
	if block.text == "output" || strings.HasPrefix(block.text, "output\n") {
		label = "tool output"
	}
	rows := []screenTranscriptRow{{
		rendered: screenCardBorder("┌─ "+label+" ", "┐", width),
		tooltip:  screenBlockTooltip(block),
	}}
	for _, line := range content {
		prefix := screenPrefix(line.kind)
		innerWidth := maxInt(1, width-4)
		for index, value := range wrapDisplay(line.text, maxInt(1, innerWidth-displayWidth(prefix))) {
			if index > 0 {
				value = strings.Repeat(" ", displayWidth(prefix)) + value
			} else {
				value = prefix + value
			}
			rows = append(rows, screenTranscriptRow{
				rendered: screenCardContent(value, line.kind, width),
				tooltip:  screenBlockTooltip(block),
			})
		}
	}
	rows = append(rows, screenTranscriptRow{rendered: screenCardBorder("└", "┘", width), tooltip: screenBlockTooltip(block)})
	return rows
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
	label := map[screenBlockKind]string{
		screenEvent:   "event",
		screenTool:    "tool",
		screenPlan:    "plan",
		screenStatus:  "status",
		screenWarning: "warning",
		screenError:   "error",
		screenInfo:    "info",
	}[block.kind]
	if label == "" {
		return ""
	}
	return label + " · " + truncateDisplay(text, 96)
}

func (u *ui) updateHover(x, y, width, height int) {
	u.hoverText = ""
	if !u.screenMode || y < 3 {
		return
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
	if y > bodyRows+2 {
		return
	}
	transcriptRows := u.screenTranscriptRows(width)
	if len(transcriptRows) == 0 {
		return
	}
	end := len(transcriptRows) - u.scrollOffset
	if end < 1 {
		end = 1
	}
	if end > len(transcriptRows) {
		end = len(transcriptRows)
	}
	start := end - bodyRows
	if start < 0 {
		start = 0
	}
	rowIndex := start + y - 3
	if rowIndex < start {
		rowIndex = start
	}
	if rowIndex >= end {
		rowIndex = end - 1
	}
	u.hoverText = transcriptRows[rowIndex].tooltip
}
