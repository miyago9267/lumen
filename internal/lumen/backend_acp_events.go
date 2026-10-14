package lumen

import (
	"encoding/json"
	"strings"
)

// acpContent 是 ACP ContentBlock（只處理 text）。
type acpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type acpToolContent struct {
	Type    string     `json:"type"`
	Content acpContent `json:"content"`
}

type acpConfigOption struct {
	ID           string `json:"id"`
	Category     string `json:"category"`
	CurrentValue any    `json:"currentValue"`
}

// acpUpdate 涵蓋 session/update 內所有 sessionUpdate 種類用到的欄位；
// 用指標區分「沒給」與「給了空值」，使只含變動欄位的 update 不會清掉先前的值。
type acpUpdate struct {
	Kind              string            `json:"sessionUpdate"`
	Content           json.RawMessage   `json:"content"`
	ToolCallID        string            `json:"toolCallId"`
	Title             *string           `json:"title"`
	ToolKind          *string           `json:"kind"`
	Status            *string           `json:"status"`
	RawInput          json.RawMessage   `json:"rawInput"`
	Entries           []acpPlanEntry    `json:"entries"`
	Used              *int64            `json:"used"`
	Size              *int64            `json:"size"`
	Cost              *acpCost          `json:"cost"`
	AvailableCommands []acpCommand      `json:"availableCommands"`
	CurrentModeID     string            `json:"currentModeId"`
	ConfigOptions     []acpConfigOption `json:"configOptions"`
}

type acpPlanEntry struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

type acpCost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type acpCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// acpTool 是依 toolCallId 累積的 tool 狀態：update 只覆蓋有給的欄位。
type acpTool struct {
	id       string
	kind     string
	title    string
	status   string
	command  string // rawInput.command（若有）
	output   string // 已輸出的文字，用來只送新增的部分
	started  bool
	finished bool
}

func (t *acpTool) merge(u acpUpdate) {
	if u.ToolKind != nil && *u.ToolKind != "" {
		t.kind = *u.ToolKind
	}
	if u.Title != nil && *u.Title != "" {
		t.title = *u.Title
	}
	if u.Status != nil && *u.Status != "" {
		t.status = *u.Status
	}
	if command := rawInputCommand(u.RawInput); command != "" {
		t.command = command
	}
}

func rawInputCommand(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var input struct {
		Command any `json:"command"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}
	switch command := input.Command.(type) {
	case string:
		return command
	case []any:
		parts := make([]string, 0, len(command))
		for _, part := range command {
			if text, ok := part.(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func acpToolKind(kind string) ToolKind {
	switch kind {
	case "execute":
		return ToolCommand
	case "edit", "delete", "move":
		return ToolEdit
	case "read":
		return ToolRead
	case "search":
		return ToolSearch
	case "fetch":
		return ToolFetch
	}
	return ToolOther
}

// acpToolTitle 是 UI 逐字顯示的 tool 標題（與 codex 的 "$ cmd"、"✎ file change"、"◇ tool" 對應）。
func acpToolTitle(kind, title, command string) string {
	switch acpToolKind(kind) {
	case ToolCommand:
		return "$ " + valueOr(command, title)
	case ToolEdit:
		return "✎ " + title
	}
	return "◇ " + title
}

func (t *acpTool) displayTitle() string { return acpToolTitle(t.kind, t.title, t.command) }

func textOf(raw json.RawMessage) string {
	var block acpContent
	if json.Unmarshal(raw, &block) != nil || block.Type != "text" {
		return ""
	}
	return block.Text
}

// toolText 取出 tool_call_update.content[] 內所有 text 內容（diff / terminal 不顯示）。
func toolText(raw json.RawMessage) string {
	var items []acpToolContent
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	var parts []string
	for _, item := range items {
		if item.Type == "content" && item.Content.Type == "text" {
			parts = append(parts, item.Content.Text)
		}
	}
	return strings.Join(parts, "")
}

// acpModeOf 從 session/new（或 load / resume）結果或 config_option_update 取出目前 mode。
func acpModeOf(currentModeID string, options []acpConfigOption) string {
	if currentModeID != "" {
		return currentModeID
	}
	for _, option := range options {
		if option.ID == "mode" || option.Category == "mode" {
			if value, ok := option.CurrentValue.(string); ok {
				return value
			}
		}
	}
	return ""
}

func acpModeWarning(mode string) string {
	if mode == "" || mode == "default" {
		return ""
	}
	return "permissions: " + mode + " (from agent settings)"
}

// acpReplay 把 session/load 重播的 update 收成 history turns（不產生 live event）。
type acpReplay struct {
	sessionID string
	turns     []historyTurn
	last      string
	tools     map[string]int
}

func newACPReplay(sessionID string) *acpReplay {
	return &acpReplay{sessionID: sessionID, tools: map[string]int{}}
}

func (r *acpReplay) hasTurn() bool { return len(r.turns) > 0 }

// cur 回傳最後一個 turn（turns 可能重新配置，不能長期持有指標）。
func (r *acpReplay) cur() *historyTurn { return &r.turns[len(r.turns)-1] }

func (r *acpReplay) ensureTurn() {
	if !r.hasTurn() {
		r.startTurn()
	}
}

func (r *acpReplay) startTurn() {
	r.turns = append(r.turns, historyTurn{Status: "completed"})
}

func (r *acpReplay) lastItem() *historyItem {
	turn := r.cur()
	return &turn.Items[len(turn.Items)-1]
}

func (r *acpReplay) add(u acpUpdate) {
	switch u.Kind {
	case "user_message_chunk":
		if r.last != "user" || !r.hasTurn() {
			r.startTurn()
			r.cur().Items = append(r.cur().Items, historyItem{Type: "userMessage", Content: []historyInput{{Type: "text"}}})
			r.last = "user"
		}
		r.lastItem().Content[0].Text += textOf(u.Content)
	case "agent_message_chunk":
		r.ensureTurn()
		if r.last != "agent" {
			r.cur().Items = append(r.cur().Items, historyItem{Type: "agentMessage"})
			r.last = "agent"
		}
		r.lastItem().Text += textOf(u.Content)
	case "tool_call", "tool_call_update":
		r.ensureTurn()
		index, known := r.tools[u.ToolCallID]
		if !known {
			item := historyItem{}
			kind := ""
			if u.ToolKind != nil {
				kind = *u.ToolKind
			}
			title := ""
			if u.Title != nil {
				title = *u.Title
			}
			switch acpToolKind(kind) {
			case ToolCommand:
				item = historyItem{Type: "commandExecution", Command: valueOr(rawInputCommand(u.RawInput), title)}
			case ToolEdit:
				item = historyItem{Type: "fileChange"}
			default:
				item = historyItem{Type: "mcpToolCall", Server: valueOr(kind, "tool"), Tool: title}
			}
			r.cur().Items = append(r.cur().Items, item)
			index = len(r.cur().Items) - 1
			r.tools[u.ToolCallID] = index
		}
		r.last = "tool"
		item := &r.cur().Items[index]
		if u.Status != nil {
			item.Status = *u.Status
		}
		if item.Type == "commandExecution" {
			if command := rawInputCommand(u.RawInput); command != "" {
				item.Command = command
			}
			item.AggregatedOutput += toolText(u.Content)
		}
	case "plan":
		r.ensureTurn()
		var lines []string
		for _, entry := range u.Entries {
			lines = append(lines, "["+entry.Status+"] "+entry.Content)
		}
		r.cur().Items = append(r.cur().Items, historyItem{Type: "plan", Text: strings.Join(lines, "\n")})
		r.last = "plan"
	}
}

func (r *acpReplay) history() []historyTurn { return r.turns }
