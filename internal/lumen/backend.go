package lumen

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// errSessionNotFound 表示 backend 找不到要接回的 session；reconnect 在 transcript 為空時可改開新 session。
var errSessionNotFound = errors.New("session not found")

// Backend 是 UI 唯一依賴的 seam。Events() 合併 backend 所有的 notification、
// server request、error 與 closed 訊號，UI event loop 只 select 這一個 channel。
type Backend interface {
	Name() string
	Caps() Capabilities
	// Events 關閉表示 backend 已結束；結束原因會先以 EvConnectionLost 送出。
	Events() <-chan Event
	// Disconnected 回報連線是否已結束，以及（若有）失敗原因。
	Disconnected() (bool, error)

	Open(ctx context.Context) error
	NewSession(cwd string, rt runtimeOverrides) (SessionInfo, error)
	ResumeSession(id, cwd string, rt runtimeOverrides) (SessionInfo, error)
	ListSessions(cwd string, limit int) ([]SessionInfo, error)
	Prompt(sessionID, text string, rt runtimeOverrides) (turnID string, err error)
	Cancel(sessionID, turnID string) error
	// ResolveApproval 以 optionID 回覆 EvApprovalRequest；optionID 必須取自該 request 的 Options。
	ResolveApproval(reqID, optionID string) error
	// RejectRequest 拒絕 backend 送來、UI 無法處理的 request（例如沒有 TTY）。
	RejectRequest(reqID, message string) error
	AnswerInput(reqID string, answers map[string][]string) error
	Close()
}

// SessionInfo 是 UI 看到的 session。P1 先沿用 codex 的 threadSummary 形狀，
// history 的 normalized 化留到 P2 一併處理。
type SessionInfo = threadSummary

// Capabilities 描述 backend 支援哪些功能，UI 先檢查再呼叫。
type Capabilities struct {
	SessionList    bool
	SessionResume  bool
	ReplayOnResume bool
	ForkAtTurn     bool
	Goal           bool
	Review         bool
	Compact        bool
	ModelList      bool
	UserInput      bool
	TokenBreakdown bool
	NativeAdmin    bool
	Rename         bool
	Archive        bool
	Delete         bool
	// SlashCatalog 為 "static"（Lumen 內建 catalog）或 "dynamic"（backend 推送）。
	SlashCatalog string
}

// CodexExtras 只有 codexBackend 實作；UI 先檢查 Caps，再 type-assert。
type CodexExtras interface {
	Fork(id string, rt runtimeOverrides) (SessionInfo, error)
	ForkAt(id, lastTurnID string, rt runtimeOverrides) (SessionInfo, error)
	Compact(id string) error
	Review(id string, target map[string]any) (turnID string, err error)
	Goal(id string) (map[string]any, error)
	SetGoal(id, objective string) (map[string]any, error)
	ClearGoal(id string) error
	ListModels() ([]codexModel, error)
	ListSkills(cwd string) ([]codexSkillEntry, error)
	ListHooks(cwd string) ([]codexHookEntry, error)
	ListMCPServers(sessionID string) ([]codexMCPServer, error)
	ListApps() ([]codexApp, error)
	ListPlugins(cwd string) ([]codexPluginMarketplace, error)
	Archive(id string) error
	Delete(id string) error
	Rename(id, name string) error
	Call(method string, params any, target any) error
}

// modelOption 是 /model 可選的一個 model；ID 是送回 backend 的值。
type modelOption struct {
	ID          string
	Name        string
	Description string
	Current     bool // backend 知道目前 model 時才會標；codex 由 UI 以 effective model 比對
}

// ModelPicker 由能列出 model 的 backend 實作（codex 的 model/list、ACP 的 model config option）。
type ModelPicker interface {
	ModelOptions() ([]modelOption, error)
}

// ConfigOption 是 backend 宣告的一個 select 型設定（ACP configOptions），UI 依它自動長出 /<id> command。
// Values 沿用 modelOption 當通用的 select value（ID=value）。Category 可為空。
// 一份 []ConfigOption 視為 immutable：要改就複製，backend 與 UI 才能安全共用 slice。
type ConfigOption struct {
	ID          string
	Name        string
	Description string
	Category    string
	Current     string
	Values      []modelOption
}

// ConfigSetter 由能在 session 內切換 ConfigOption 的 backend 實作（ACP）。
// 只能由使用者明確下的 /<id> <value> 觸發；回傳 backend 認可的 value ID。
type ConfigSetter interface {
	SetConfigOption(sessionID, id, value string) (string, error)
}

// withCurrent 回傳 current 改成 value 的複本（不改動原本的 Values）。
func (o ConfigOption) withCurrent(value string) ConfigOption {
	o.Current = value
	values := make([]modelOption, len(o.Values))
	for i, item := range o.Values {
		item.Current = item.ID == value
		values[i] = item
	}
	o.Values = values
	return o
}

func (o ConfigOption) valueIDs() string {
	ids := make([]string, 0, len(o.Values))
	for _, item := range o.Values {
		ids = append(ids, item.ID)
	}
	return strings.Join(ids, ", ")
}

// Resolve 把使用者輸入對到一個 value：(1) value 完全相同；(2) value 不分大小寫唯一相同；
// (3) display name 不分大小寫唯一相同。name 命中多個 value 時回 ambiguous，不猜。
func (o ConfigOption) Resolve(query string) (modelOption, error) {
	query = strings.TrimSpace(query)
	for _, item := range o.Values {
		if item.ID == query {
			return item, nil
		}
	}
	for _, match := range []func(modelOption) string{
		func(item modelOption) string { return item.ID },
		func(item modelOption) string { return item.Name },
	} {
		var hits []modelOption
		for _, item := range o.Values {
			if query != "" && strings.EqualFold(match(item), query) {
				hits = append(hits, item)
			}
		}
		switch len(hits) {
		case 1:
			return hits[0], nil
		case 0:
		default:
			ids := make([]string, 0, len(hits))
			for _, item := range hits {
				ids = append(ids, item.ID)
			}
			return modelOption{}, fmt.Errorf("ambiguous %s value %q; matches: %s", o.ID, query, strings.Join(ids, ", "))
		}
	}
	return modelOption{}, fmt.Errorf("unknown %s value %q; available: %s", o.ID, query, o.valueIDs())
}

// configValue 取出第一個指定 category 的 option 目前值；沒有則回傳空字串。
func configValue(options []ConfigOption, category string) string {
	for _, option := range options {
		if option.Category == category {
			return option.Current
		}
	}
	return ""
}

// Event 是 backend 送給 UI 的 normalized event（sealed interface）。
type Event interface{ isEvent() }

type ToolKind string

const (
	ToolCommand ToolKind = "command"
	ToolEdit    ToolKind = "edit"
	ToolRead    ToolKind = "read"
	ToolSearch  ToolKind = "search"
	ToolFetch   ToolKind = "fetch"
	ToolMCP     ToolKind = "mcp"
	ToolOther   ToolKind = "other"
)

type ToolStatus string

const (
	ToolOK        ToolStatus = "ok"
	ToolFailed    ToolStatus = "failed"
	ToolCancelled ToolStatus = "cancelled"
)

type TurnStatus string

const (
	TurnCompleted   TurnStatus = "completed"
	TurnInterrupted TurnStatus = "interrupted"
	TurnFailed      TurnStatus = "failed"
	TurnRefused     TurnStatus = "refused"
	TurnMaxTokens   TurnStatus = "maxTokens"
)

type NoticeLevel string

const (
	NoticeInfo  NoticeLevel = "info"
	NoticeWarn  NoticeLevel = "warn"
	NoticeError NoticeLevel = "error"
)

// ApprovalOptionKind 讓 UI 不必認得 backend 專屬的 option id。
type ApprovalOptionKind string

const (
	OptionAllowOnce    ApprovalOptionKind = "allowOnce"
	OptionAllowAlways  ApprovalOptionKind = "allowAlways"
	OptionRejectOnce   ApprovalOptionKind = "rejectOnce"
	OptionRejectAlways ApprovalOptionKind = "rejectAlways"
	OptionCancel       ApprovalOptionKind = "cancel"
	OptionOther        ApprovalOptionKind = "other"
)

// CancelApprovalID 是 ResolveApproval 表示「使用者取消」的保留值；不會與任何 backend 的 option id 相同
// （例如 Gemini 自己就有 id 為 "cancel" 的 option）。
const CancelApprovalID = "\x00lumen:cancel"

type ApprovalOption struct {
	ID    string
	Label string
	Kind  ApprovalOptionKind
}

type PlanItem struct {
	Text   string
	Status string
}

// UsageBreakdown 只有提供 token 拆分的 backend（codex）才填。
type UsageBreakdown struct {
	Input         int64
	Output        int64
	CacheRead     int64
	CacheCreation int64
}

type EvTurnStarted struct {
	SessionID string
	TurnID    string
}

type EvAssistantDelta struct {
	TurnID string
	Text   string
}

type EvReasoningStarted struct{ TurnID string }

type EvReasoningCompleted struct{ TurnID string }

type EvReasoningDelta struct {
	TurnID string
	Text   string
}

// EvToolStarted.Title 是 UI 逐字顯示的文字（例如 "$ ls"、"✎ file change"）；
// Command 只供 recap 使用。
type EvToolStarted struct {
	ToolID  string
	Kind    ToolKind
	Title   string
	Command string
}

type EvToolOutput struct {
	ToolID string
	Text   string
}

// EvToolCompleted.Status 只供邏輯判斷；Label 是 UI 逐字顯示的原始字串，空字串表示不顯示。
type EvToolCompleted struct {
	ToolID string
	Status ToolStatus
	Label  string
}

type EvPlanUpdated struct{ Items []PlanItem }

type EvSessionTitle struct {
	SessionID string
	Title     string
}

// UsageCost 是 backend 回報的累計花費（ACP usage_update.cost）。
type UsageCost struct {
	Amount   float64
	Currency string
}

// EvUsage.Size 為 nil 表示 backend 沒有回報 context window；Cost 為 nil 表示沒有花費資訊。
type EvUsage struct {
	Used      int64
	Size      *int64
	Breakdown *UsageBreakdown
	Cost      *UsageCost
}

// SlashCommand 是 backend 動態宣告的 slash command（ACP available_commands_update）。
type SlashCommand struct {
	Name        string
	Description string
}

// EvCommandsUpdated 取代目前的動態 slash catalog。
type EvCommandsUpdated struct{ Commands []SlashCommand }

// EvModeWarning 回報 backend 的 permission mode 狀態；Text 為空表示沒有警示（mode 為 default）。
// Text 由 UI 持續顯示在 status bar。
type EvModeWarning struct {
	Mode string
	Text string
}

// EvConfigOptions 整份取代 UI 持有的 config option 清單（session 開啟 / 接回、config_option_update、
// set_config_option 回應）。選項消失代表 agent 不再提供，UI 不保留舊值。
type EvConfigOptions struct{ Options []ConfigOption }

// EvTurnCompleted.Label 是 UI 逐字顯示的原始字串（空字串視為 completed）。
type EvTurnCompleted struct {
	TurnID string
	Status TurnStatus
	Label  string
	Err    error
}

type EvNotice struct {
	Level NoticeLevel
	Text  string
}

// EvApprovalRequest.Explicit 為 true 時（ACP），UI 不得預設任何選項：Enter 不做決定，
// 使用者必須明確按數字鍵選 Options 之一，或以 Esc / Ctrl-C 取消（回 CancelApprovalID）。
type EvApprovalRequest struct {
	ReqID    string
	TurnID   string
	Title    string
	CWD      string
	Reason   string
	Options  []ApprovalOption
	Explicit bool
}

// EvApprovalExpired 表示 backend 已自行結束某張 approval（例如 ACP permission 逾時而回 cancelled）；
// UI 若仍顯示該 request 的 card 必須清掉，並顯示 Text。
type EvApprovalExpired struct {
	ReqID string
	Text  string
}

type EvInputRequest struct {
	ReqID     string
	TurnID    string
	Questions []userInputQuestion
}

// EvRequestRejected 表示 backend 已拒絕一個不屬於目前 session 或格式錯誤的 request，UI 只負責顯示。
type EvRequestRejected struct {
	ReqID   string
	Message string
}

type EvConnectionLost struct{ Err error }

func (EvTurnStarted) isEvent()        {}
func (EvAssistantDelta) isEvent()     {}
func (EvReasoningStarted) isEvent()   {}
func (EvReasoningCompleted) isEvent() {}
func (EvReasoningDelta) isEvent()     {}
func (EvToolStarted) isEvent()        {}
func (EvToolOutput) isEvent()         {}
func (EvToolCompleted) isEvent()      {}
func (EvPlanUpdated) isEvent()        {}
func (EvSessionTitle) isEvent()       {}
func (EvUsage) isEvent()              {}
func (EvTurnCompleted) isEvent()      {}
func (EvNotice) isEvent()             {}
func (EvApprovalRequest) isEvent()    {}
func (EvApprovalExpired) isEvent()    {}
func (EvInputRequest) isEvent()       {}
func (EvRequestRejected) isEvent()    {}
func (EvConnectionLost) isEvent()     {}
func (EvCommandsUpdated) isEvent()    {}
func (EvModeWarning) isEvent()        {}
func (EvConfigOptions) isEvent()      {}
