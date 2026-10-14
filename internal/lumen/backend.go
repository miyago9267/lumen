package lumen

import "context"

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
	ModelSwitch    bool
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

// EvUsage.Size 為 nil 表示 backend 沒有回報 context window。
type EvUsage struct {
	Used      int64
	Size      *int64
	Breakdown *UsageBreakdown
}

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

type EvApprovalRequest struct {
	ReqID   string
	TurnID  string
	Title   string
	CWD     string
	Reason  string
	Options []ApprovalOption
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
func (EvInputRequest) isEvent()       {}
func (EvRequestRejected) isEvent()    {}
func (EvConnectionLost) isEvent()     {}
