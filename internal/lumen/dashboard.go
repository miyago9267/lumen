package lumen

import (
	"fmt"
	"strings"
)

const maxDashboardSessions = 9

func (u *ui) toggleDashboard() error {
	if u.dashboard {
		u.dashboard = false
		u.dashboardError = ""
		u.hoverText = ""
		return nil
	}
	return u.openDashboard()
}

func (u *ui) openDashboard() error {
	if !u.screenMode {
		return fmt.Errorf("dashboard requires full-screen mode; rerun without --minimal")
	}
	u.dashboard = true
	u.dashboardIndex = 0
	u.dashboardError = ""
	u.hoverText = ""
	u.scrollOffset = 0
	if err := u.refreshDashboard(); err != nil {
		u.dashboardError = err.Error()
	}
	return nil
}

func (u *ui) refreshDashboard() error {
	if u.client == nil {
		u.dashboardRows = []threadSummary{u.thread}
		return nil
	}
	threads, err := u.client.ListThreads("", maxDashboardSessions)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	seen := make(map[string]bool, len(threads))
	rows := make([]threadSummary, 0, maxDashboardSessions)
	for _, thread := range threads {
		if thread.ID == "" || seen[thread.ID] {
			continue
		}
		seen[thread.ID] = true
		rows = append(rows, thread)
		if len(rows) == maxDashboardSessions {
			break
		}
	}
	if u.thread.ID != "" && !seen[u.thread.ID] {
		if len(rows) == maxDashboardSessions {
			rows = rows[:maxDashboardSessions-1]
		}
		rows = append([]threadSummary{u.thread}, rows...)
	}
	if len(rows) == 0 {
		rows = append(rows, u.thread)
	}
	u.dashboardRows = rows
	if u.dashboardIndex >= len(rows) {
		u.dashboardIndex = len(rows) - 1
	}
	return nil
}

func (u *ui) handleDashboardKey(key keyEvent) error {
	switch key.typ {
	case keyEscape, keyCtrlX:
		return u.toggleDashboard()
	case keyUp:
		if u.dashboardIndex > 0 {
			u.dashboardIndex--
		}
	case keyDown:
		if u.dashboardIndex+1 < len(u.dashboardRows) {
			u.dashboardIndex++
		}
	case keyPageUp:
		u.dashboardIndex -= 3
		if u.dashboardIndex < 0 {
			u.dashboardIndex = 0
		}
	case keyPageDown:
		u.dashboardIndex += 3
		if u.dashboardIndex >= len(u.dashboardRows) {
			u.dashboardIndex = maxInt(0, len(u.dashboardRows)-1)
		}
	case keyCtrlU:
		u.dashboardIndex -= 3
		if u.dashboardIndex < 0 {
			u.dashboardIndex = 0
		}
	case keyRune:
		if key.rune >= '1' && key.rune <= '9' {
			index := int(key.rune - '1')
			if index < len(u.dashboardRows) {
				return u.attachDashboard(index)
			}
		}
		if key.rune == 'n' || key.rune == 'N' {
			return u.startDashboardSession()
		}
		if key.rune == 'r' || key.rune == 'R' {
			u.dashboardError = ""
			if err := u.refreshDashboard(); err != nil {
				u.dashboardError = err.Error()
			}
		}
	case keyEnter:
		return u.attachDashboard(u.dashboardIndex)
	case keyMouseClick:
		if index, ok := u.dashboardIndexFromMouse(key.mouseY); ok {
			u.dashboardIndex = index
		}
	}
	return nil
}

func (u *ui) attachDashboard(index int) error {
	if index < 0 || index >= len(u.dashboardRows) {
		return nil
	}
	if u.busy {
		u.dashboardError = "active turn still running · attach after it completes"
		return nil
	}
	thread := u.dashboardRows[index]
	if thread.ID == "" || thread.ID == u.thread.ID {
		u.dashboard = false
		u.dashboardError = ""
		return nil
	}
	if u.client == nil {
		return fmt.Errorf("session attach requires app-server")
	}
	resumed, err := u.client.ResumeThread(thread.ID, u.overrides)
	if err != nil {
		u.dashboardError = "attach failed · " + err.Error()
		return nil
	}
	u.endScreenTurn()
	u.clearReplyTarget()
	u.thread = resumed
	u.cwd = valueOr(resumed.CWD, u.cwd)
	u.branch = gitBranch(u.cwd)
	u.clearScreenTranscript()
	u.scrollOffset = 0
	u.hoverText = ""
	u.usage = threadUsage{}
	u.recap = turnRecap{}
	u.turnID = ""
	u.assistant = false
	u.tool = false
	u.toolOutput = false
	u.showWelcome = len(resumed.Turns) == 0
	u.dashboard = false
	u.dashboardError = ""
	u.renderHistory()
	u.screenAdd(screenEvent, "attached · "+shortID(resumed.ID))
	return nil
}

func (u *ui) startDashboardSession() error {
	if u.busy {
		u.dashboardError = "active turn still running · start after it completes"
		return nil
	}
	if u.client == nil {
		u.dashboardError = "new session requires app-server"
		return nil
	}
	thread, err := u.client.StartThread(u.cwd, u.overrides)
	if err != nil {
		u.dashboardError = "new session failed · " + err.Error()
		return nil
	}
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
	u.assistant = false
	u.tool = false
	u.toolOutput = false
	u.showWelcome = len(thread.Turns) == 0
	u.dashboard = false
	u.dashboardError = ""
	u.screenAdd(screenEvent, "new session · "+shortID(thread.ID))
	return nil
}

func (u *ui) screenDashboardRows(width int) []screenTranscriptRow {
	rows := []screenTranscriptRow{{
		rendered: screenStyledLine(screenInfo, "dashboard  ·  session supervisor  ·  all sessions", width),
		tooltip:  "resume picker · all top-level Codex sessions",
	}, {
		rendered: screenStyledLine(screenStatus, "  resume history · attach · new session · refresh", width),
		tooltip:  "dashboard controls",
	}}
	if u.dashboardError != "" {
		rows = append(rows, screenTranscriptRow{
			rendered: screenStyledLine(screenWarning, "⚠ "+u.dashboardError, width),
			tooltip:  "dashboard · " + u.dashboardError,
		})
	}
	if len(u.dashboardRows) == 0 {
		rows = append(rows, screenTranscriptRow{
			rendered: screenStyledLine(screenStatus, "· no sessions", width),
			tooltip:  "dashboard · no sessions",
		})
		return rows
	}
	for index, thread := range u.dashboardRows {
		marker := "  "
		kind := screenStatus
		if index == u.dashboardIndex {
			marker = "▸ "
			kind = screenUser
		}
		if thread.ID == u.thread.ID {
			marker = "● "
			if index == u.dashboardIndex {
				marker = "◉ "
			}
		}
		name := valueOr(strings.TrimSpace(thread.Name), "unnamed")
		state := valueOr(threadStatusLabel(thread), lastTurnStatus(thread))
		state = valueOr(state, "idle")
		cwd := valueOr(strings.TrimSpace(thread.CWD), "cwd unknown")
		cwd = truncateDisplay(sanitizeText(displayCWD(cwd)), 24)
		preview := valueOr(threadPreview(thread), "no preview yet")
		line := fmt.Sprintf("%s%d  %-16s  %-10s  %-24s  %s", marker, index+1, truncateDisplay(name, 16), truncateDisplay(state, 10), cwd, preview)
		rows = append(rows, screenTranscriptRow{
			rendered: screenStyledLine(kind, truncateDisplay(line, width), width),
			tooltip:  fmt.Sprintf("session %s · %s · %s", shortID(thread.ID), name, preview),
		})
	}
	return rows
}

func threadPreview(thread threadSummary) string {
	if preview := strings.TrimSpace(thread.Preview); preview != "" {
		return truncateDisplay(sanitizeText(strings.ReplaceAll(preview, "\n", " ")), 56)
	}
	for turnIndex := len(thread.Turns) - 1; turnIndex >= 0; turnIndex-- {
		turn := thread.Turns[turnIndex]
		for itemIndex := len(turn.Items) - 1; itemIndex >= 0; itemIndex-- {
			item := turn.Items[itemIndex]
			if item.Type == "agentMessage" && strings.TrimSpace(item.Text) != "" {
				return truncateDisplay(sanitizeText(strings.ReplaceAll(item.Text, "\n", " ")), 56)
			}
		}
	}
	return ""
}

func lastTurnStatus(thread threadSummary) string {
	if len(thread.Turns) == 0 {
		return ""
	}
	return thread.Turns[len(thread.Turns)-1].Status
}

func threadStatusLabel(thread threadSummary) string {
	switch status := thread.Status.(type) {
	case string:
		return status
	case map[string]any:
		for _, key := range []string{"type", "status", "state"} {
			if value, ok := status[key].(string); ok {
				return value
			}
		}
	}
	return ""
}

func (u *ui) dashboardIndexFromMouse(y int) (int, bool) {
	// The first two rows are the dashboard title and controls; each session is
	// rendered as one row below them. Mouse coordinates are 1-based.
	base := 5
	if u.dashboardError != "" {
		base++
	}
	index := y - base
	if index < 0 || index >= len(u.dashboardRows) {
		return 0, false
	}
	return index, true
}
