package lumen

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// buildBackend 依 registry 建立尚未 Open 的 backend；測試可用 ui.openBackend 取代。
func (u *ui) buildBackend(name string) (Backend, error) {
	if u.openBackend != nil {
		return u.openBackend(name)
	}
	if u.registry == nil {
		return nil, fmt.Errorf("backend switching is not available")
	}
	spec, ok := u.registry.Spec(name)
	if !ok {
		return nil, fmt.Errorf("unknown backend %q", name)
	}
	return spec.newBackend()
}

// newSupervisor 建立 reconnect supervisor。factory 在每次重連時才讀 u.serverFactory，
// 因此 /backend 切換後重連的是目前的 backend。切換只在 backend 連線中進行，與重連不會並行。
func (u *ui) newSupervisor() *sessionSupervisor {
	return newSessionSupervisor(func() (Backend, error) {
		factory := u.serverFactory
		if factory == nil {
			factory = startCodexBackend
		}
		return factory()
	})
}

// backendBlockReason 回傳目前不能切換 backend 的原因；空字串表示可切換。
func (u *ui) backendBlockReason() string {
	switch {
	case u.backend == nil:
		return "backend is reconnecting · wait for the connection first"
	case u.busy:
		return "a turn is running · interrupt it first"
	case u.approval != nil:
		return "an approval is pending · interrupt first"
	case u.inputRequest != nil:
		return "a user input request is pending · interrupt first"
	case len(u.queuedPrompts) > 0 || u.cancelAndSend != "":
		return "prompts are queued · interrupt first"
	}
	return ""
}

func (u *ui) unknownBackendMessage(name string) string {
	return fmt.Sprintf("unknown backend %q (available: %s)", name, strings.Join(u.registry.Names(), ", "))
}

// commandBackend 處理 /backend：無參數開 picker，/backend <name> 直接切換。
func (u *ui) commandBackend(args string) error {
	if u.registry == nil {
		u.commandMessage("/backend is not available in this session", yellow)
		return nil
	}
	if reason := u.backendBlockReason(); reason != "" {
		u.commandMessage("/backend refused: "+reason, yellow)
		return nil
	}
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return u.openBackendPicker()
	}
	return u.switchBackend(fields[0])
}

// switchBackend 做 atomic swap：先 Open + NewSession 新 backend，成功後才關閉舊 backend；
// 任何一步失敗都保留舊 backend 與目前 session。切換一律是新 session，不轉移上下文，也不帶 codex runtime overrides。
func (u *ui) switchBackend(name string) error {
	if u.registry == nil {
		u.commandMessage("/backend is not available in this session", yellow)
		return nil
	}
	if reason := u.backendBlockReason(); reason != "" {
		u.commandMessage("/backend refused: "+reason, yellow)
		return nil
	}
	spec, ok := u.registry.Spec(name)
	if !ok {
		u.commandMessage(u.unknownBackendMessage(name), yellow)
		return nil
	}
	old := u.backend
	if old.Name() == spec.Name {
		u.commandMessage("already using backend "+spec.Name, muted)
		return nil
	}

	next, err := u.buildBackend(spec.Name)
	if err != nil {
		u.commandMessage(fmt.Sprintf("backend switch to %s failed: %v", spec.Name, err), red)
		return nil
	}
	if err := next.Open(context.Background()); err != nil {
		next.Close()
		u.commandMessage(fmt.Sprintf("backend switch to %s failed: %v", spec.Name, err), red)
		return nil
	}
	session, err := next.NewSession(u.cwd, runtimeOverrides{})
	if err != nil {
		next.Close()
		u.commandMessage(fmt.Sprintf("backend switch to %s failed: %v", spec.Name, err), red)
		return nil
	}

	u.backend = next
	u.serverFactory = func() (Backend, error) { return u.buildBackend(spec.Name) }
	old.Close()
	u.adoptSwitchedBackend(session)
	u.commandMessage(fmt.Sprintf("backend: %s → %s · new session", old.Name(), next.Name()), cyan)
	return nil
}

// adoptSwitchedBackend 套用新 backend 的新 session：舊 transcript 保留，backend 專屬狀態全部重置。
// slash catalog、dock 與 capability-gated affordance 都是依 u.backend 即時計算，不需另外重建。
func (u *ui) adoptSwitchedBackend(session SessionInfo) {
	u.endScreenTurn()
	u.clearReplyTarget()
	u.thread = session
	u.cwd = valueOr(session.CWD, u.cwd)
	u.branch = gitBranch(u.cwd)
	u.overrides = runtimeOverrides{}
	u.agentCommands = nil
	u.permissionNote = ""
	u.usage = threadUsage{}
	u.recap = turnRecap{}
	u.turnID = ""
	u.busy = false
	u.assistant = false
	u.tool = false
	u.toolOutput = false
	u.reasoningActive = false
	u.reasoningShown = false
	u.reasoningFrame = 0
	u.cancelAndSend = ""
	u.connectionError = ""
	u.dashboard = false
	u.dashboardError = ""
	u.backendPicker = false
	u.scrollOffset = 0
	u.hoverText = ""
	u.commandPopupIndex = 0
	u.showWelcome = len(u.transcript.entries) == 0
}

// capsSummary 以短字串列出 backend 提供的選用功能。
func capsSummary(caps Capabilities) string {
	var features []string
	for _, item := range []struct {
		name string
		on   bool
	}{
		{"list", caps.SessionList}, {"resume", caps.SessionResume}, {"fork", caps.ForkAtTurn},
		{"goal", caps.Goal}, {"review", caps.Review}, {"compact", caps.Compact}, {"model", caps.ModelList},
	} {
		if item.on {
			features = append(features, item.name)
		}
	}
	if len(features) == 0 {
		return "prompts only"
	}
	return strings.Join(features, " ")
}

// backendSummary 是 picker 一列的說明：種類（只顯示 command basename，不印 args / env）；
// 目前的 backend 另附即時 capability。
func (u *ui) backendSummary(spec backendSpec) string {
	summary := "codex native"
	if spec.Type == backendTypeACP {
		summary = "acp · " + filepath.Base(spec.Command)
	}
	if u.backend != nil && u.backend.Name() == spec.Name {
		summary += " · " + capsSummary(u.backend.Caps())
	}
	return summary
}

func (u *ui) backendPickerNames() []string {
	if u.registry == nil {
		return nil
	}
	return u.registry.Names()
}

func (u *ui) openBackendPicker() error {
	names := u.backendPickerNames()
	if !u.screenMode {
		lines := make([]string, 0, len(names)+1)
		for _, name := range names {
			spec, _ := u.registry.Spec(name)
			marker := "  "
			if u.backend != nil && u.backend.Name() == name {
				marker = "● "
			}
			lines = append(lines, fmt.Sprintf("%s%-12s %s", marker, name, u.backendSummary(spec)))
		}
		lines = append(lines, "switch with /backend <name>")
		u.showCommandOutput("backends", lines)
		return nil
	}
	u.backendPicker = true
	u.backendPickerIndex = 0
	for index, name := range names {
		if u.backend != nil && u.backend.Name() == name {
			u.backendPickerIndex = index
		}
	}
	u.hoverText = ""
	u.scrollOffset = 0
	return nil
}

func (u *ui) handleBackendPickerKey(key keyEvent) error {
	names := u.backendPickerNames()
	switch key.typ {
	case keyEscape, keyCtrlC, keyCtrlX:
		u.backendPicker = false
	case keyUp:
		if u.backendPickerIndex > 0 {
			u.backendPickerIndex--
		}
	case keyDown:
		if u.backendPickerIndex+1 < len(names) {
			u.backendPickerIndex++
		}
	case keyEnter:
		return u.pickBackend(u.backendPickerIndex)
	case keyRune:
		if key.rune >= '1' && key.rune <= '9' {
			return u.pickBackend(int(key.rune - '1'))
		}
	}
	return nil
}

func (u *ui) pickBackend(index int) error {
	names := u.backendPickerNames()
	if index < 0 || index >= len(names) {
		return nil
	}
	u.backendPicker = false
	return u.switchBackend(names[index])
}

func (u *ui) screenBackendPickerRows(width int) []screenTranscriptRow {
	rows := []screenTranscriptRow{{
		rendered: screenStyledLine(screenInfo, "backends  ·  switching starts a new session", width),
		tooltip:  "backend picker",
	}, {
		rendered: screenStyledLine(screenStatus, "  the current transcript stays on screen · nothing is carried over", width),
		tooltip:  "backend picker controls",
	}}
	for index, name := range u.backendPickerNames() {
		spec, _ := u.registry.Spec(name)
		current := u.backend != nil && u.backend.Name() == name
		marker := "  "
		kind := screenStatus
		if index == u.backendPickerIndex {
			marker = "▸ "
			kind = screenUser
		}
		label := ""
		if current {
			marker = "● "
			if index == u.backendPickerIndex {
				marker = "◉ "
			}
			label = " (current)"
		}
		line := fmt.Sprintf("%s%d  %-14s %s", marker, index+1, name+label, u.backendSummary(spec))
		rows = append(rows, screenTranscriptRow{
			rendered: screenStyledLine(kind, truncateDisplay(line, width), width),
			tooltip:  "backend " + name,
		})
	}
	return rows
}
