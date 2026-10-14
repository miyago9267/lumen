---
id: spec-lumen-backend-adapter
title: Lumen pluggable backend (ACP client)
status: approved
created: 2026-10-14
updated: 2026-10-14
author: Miyago
approved_by: Miyago
tags: [lumen, tui, acp, backend, claude-code, codex]
priority: high
depends_on: [spec-lumen]
---

<!-- markdownlint-disable MD025 -->
# Lumen pluggable backend (ACP client)
<!-- markdownlint-enable MD025 -->

> 狀態：approved（Miyago，2026-10-14）。已接受 ADR-4 自寫 ACP client、JSON config、預設改 Claude。
> 研究驗證日：2026-10-14。T2.0 spike 已完成（`claude-agent-acp@0.90.0`，2026-10-14），結果已折回本文；
> 仍標 `unverified` 的項目是 spike 未能驗證的假設，不是事實（見文末 Open questions）。

## Goal

把 Lumen 從「`codex app-server` 專用 frontend」改成「純 TUI frontend + 可替換
backend」：

- UI 只消費 Lumen 自己的 normalized event model 與 `Backend` interface，不再直接
  解析 codex 的 method name。
- 通用 backend 走 Agent Client Protocol (ACP)：Lumen 是 ACP client，任何 ACP
  agent subprocess 都能當 backend。
- Codex 保留現有 native `codex app-server` path，所有 Codex 專屬功能行為不變。
- 預設 backend 改為 Claude Code（經 ACP adapter），用 `--backend`、
  `LUMEN_BACKEND` 或 in-app `/backend` 選擇。

## Non-goals

- 不重新決定 plugin protocol（已定：ACP）。不另造 Lumen 自有 plugin protocol。
- 不把 Codex 改走 ACP（Codex-via-ACP 是 optional，不在本 spec 範圍）。
- 不實作 ACP agent 端（Lumen 不對外提供 ACP server）。
- 第一個 slice 不實作 `fs/*`、`terminal/*` client method，不實作 MCP server 注入、
  image/audio prompt、`session/set_mode` UI。
- 不做 backend 熱切換「保留同一個 session」；切換一律開新 session。
- 不重做 UI 視覺，不改 Hybrid UX contract（見 `docs/specs/lumen/SPEC.md`）。
- 不做 plugin 市集、動態下載、backend 之間的 transcript 轉移。

## 現況耦合（live verified，2026-10-14，`internal/lumen/`）

| 位置 | 耦合內容 |
| --- | --- |
| `ui.go:986-1160` `handleNotification` | 直接 switch codex method：`turn/started`、`item/started`、`item/agentMessage/delta`(1000)、`item/commandExecution/outputDelta`(1021)、`item/completed`(1050)、`turn/plan/updated`(1052)、`thread/name/updated`(1077)、`thread/tokenUsage/updated`(1085)、`turn/completed`(1114)、`error`(1124)、`warning`(1131) |
| `ui.go:1161-1295` | `handleItemStarted/Completed` 依 item type `commandExecution` / `fileChange` / `mcpToolCall` / `reasoning` 分支 |
| `ui.go:1295-1356` `handleServerRequest` | `item/commandExecution/requestApproval`(1296)、`item/fileChange/requestApproval`(1318)、`item/permissions/requestApproval`(1334，目前回空 grant)、`item/tool/requestUserInput`(1356) |
| `ui.go:1400` `validateServerRequest` | 以 codex `threadId`/`turnId` 比對 |
| `ui.go:1455-1475` | approval 回應 `{"decision": accept/acceptForSession/decline/cancel}`，受 `availableDecisions` 限制 |
| `ui.go:1594` | user-input 回應 `{"answers": {id: {answers: []}}}` |
| `ui.go:466-500`、`screen.go:172-210` | event loop 直接 select `client.requests` / `client.notifications` / `client.errors` / `client.closed` |
| `app.go:276` `type appServer` | JSON-RPC transport + 約 40 個 method（`StartThread`:388、`ResumeThread`:412、`ListThreads`:433、`ListModels`:461、`ForkThreadAt`:591、`CompactThread`:631、`StartReview`:635、`Get/Set/ClearThreadGoal`:651-674、`StartTurn`:693、`Interrupt`:737、`Call`:744、`respond`:794） |
| `app.go:164` `threadSummary` | codex thread shape（含 `Turns []historyTurn`，`replayThreadHistory` 使用） |
| `native_command_handlers.go` | 20 處 `u.client.*`：`ListModels`:105、`ListSkills`:154、`ListHooks`:183、`StartReview`:229、`SetThreadName`:249、`StartThread`:264、`ArchiveThread`:283、`DeleteThread`:301、`ResumeThread`:321、`ForkThread`:337、`ForkThreadAt`:361、`CompactThread`:378、goal:392-413、`Interrupt`:473、`ListMCPServers`:485、`ListApps`:509、`ListPlugins`:533、`Call`:566 |
| `dashboard.go:40,142,178` | `ListThreads`、`ResumeThread`、`StartThread` |
| `session_supervisor.go` | `factory func() (*appServer, error)`、`reconnectWithSnapshot`（`Initialize` -> `ResumeThread` -> 失敗且 transcript 有內容則 `StartThread`） |
| `lifecycle.go:20,225` | `reconnectResult.client *appServer`、`adoptReconnected` |
| `screen.go:128` | `handleDisconnect(client *appServer, ...)` |
| `native_commands.go:17` | `slashCommandCatalog` 對齊 Codex TUI，固定 catalog |
| `app.go:99-130` `parseOptions` | `--resume` / `--continue` / `--model` / `--reasoning-effort` / `--sandbox` / `--approval-policy` 都是 codex 語意；無 `--backend` |

事實：repo 目前只有一個 Go package、依賴只有 `golang.org/x/term`、`go 1.26.0`
（`go.mod`）。

## Research：ACP（primary sources，2026-10-14 取得）

### 版本

- `protocolVersion` 是單一整數 MAJOR，client 在 `initialize` 送自己支援的最新版，
  agent 回同值或它支援的最新版；不支援就關線並告知使用者。目前文件範例為 `1`。
  來源：<https://agentclientprotocol.com/protocol/initialization>
- Schema release：`Schema v1.25.0 (Latest)`，`Rust Crate v1.11.0`，頁面日期 `09 Oct`
  （頁面未標年份，推定 2026）。同頁有 `Schema v2.0.0-alpha.8 (Pre-release)`。
  Lumen 只對 `protocolVersion: 1`。來源：
  <https://github.com/agentclientprotocol/agent-client-protocol/releases>
- `claude-agent-acp` 有 opt-in v2 模式（`CLAUDE_AGENT_ACP_EXPERIMENTAL_V2=1`）；
  Lumen 不啟用。來源：<https://github.com/agentclientprotocol/claude-agent-acp>

### Method 清單（來源：<https://agentclientprotocol.com/protocol/overview>）

- Agent methods：`initialize`、`authenticate`、`session/new`、`session/prompt`
  為 baseline；optional：`session/load`（需 `loadSession`）、`session/set_mode`、
  `logout`；notification：`session/cancel`。
- Client methods：`session/request_permission` 為 baseline；optional：
  `fs/read_text_file`、`fs/write_text_file`、`terminal/{create,output,release,wait_for_exit,kill}`、
  `elicitation/create`；notification：`session/update`、`elicitation/complete`。
- Session setup（<https://agentclientprotocol.com/protocol/session-setup>）：
  `session/new{cwd, mcpServers}` -> `sessionId`；`session/load{sessionId, cwd, mcpServers}`
  會把歷史用 `session/update` 重播後回空結果；`session/resume`（需
  `sessionCapabilities.resume`）不重播歷史；`session/close`（需
  `sessionCapabilities.close`）。`cwd` 必須是絕對路徑。`mcpServers` 欄位必填（Lumen 一律送空陣列；
  `claude-agent-acp@0.90.0` 實測接受空陣列，是否允許省略 `unverified`、Lumen 不省略）。
- `session/list{cwd?, cursor?}` -> `sessions[{sessionId, cwd, title?, updatedAt?}]`、
  `nextCursor?`；需 `agentCapabilities.sessionCapabilities.list`，沒有就不得呼叫。
  來源：<https://agentclientprotocol.com/protocol/session-list>
- `sessionCapabilities` 各項為 `{}`（支援）或 null/省略：`list`、`resume`、`close`、
  `delete`、`additionalDirectories`。來源：
  <https://agentclientprotocol.com/protocol/schema>
- `session/prompt{sessionId, prompt: ContentBlock[]}` -> `{stopReason}`；
  `stopReason` ∈ `end_turn | max_tokens | max_turn_requests | refusal | cancelled`。
- `session/update` 的 `sessionUpdate` 值：`user_message_chunk`、`agent_message_chunk`、
  `agent_thought_chunk`、`tool_call`、`tool_call_update`、`plan`、
  `available_commands_update`、`current_mode_update`、`config_option_update`、
  `session_info_update`、`usage_update`、`notice`（需 `session.notices`）、
  `compaction_update` / `compaction_summary_chunk`（需 `session.compaction`）。
  來源：<https://agentclientprotocol.com/protocol/prompt-turn>
- `tool_call`：`toolCallId`、`title`、`kind`（`read|edit|delete|move|search|execute|think|fetch|switch_mode|other`）、
  `status`（`pending|in_progress|completed|failed`）、`content[]`（`content` / `diff{path,oldText?,newText}` /
  `terminal{terminalId}`）、`locations[{path,line?}]`、`rawInput`、`rawOutput`。
  `tool_call_update` 只送變動欄位。來源：
  <https://agentclientprotocol.com/protocol/tool-calls>
- `session/request_permission{sessionId, toolCall, options[{optionId,name,kind}]}`；
  `kind` ∈ `allow_once|allow_always|reject_once|reject_always`；回應
  `{outcome:"selected", optionId}` 或 `{outcome:"cancelled"}`。
- Cancel：client 送 `session/cancel` notification；SHOULD 把未完成 tool call 標
  `cancelled`；MUST 對所有 pending `session/request_permission` 回 `cancelled`；agent 之後
  仍可送 update，但必須在回 `session/prompt` 前送完，最終 `stopReason: "cancelled"`；client
  SHOULD 仍接受 cancel 之後到達的 tool update。
- `usage_update{used, size, cost?{amount,currency}}`：`used` = context 內 token，
  `size` = context window，`cost` = 累計成本。來源：schema 頁。
- Slash commands：`available_commands_update.availableCommands[{name, description, input?{hint}}]`；
  client 以一般文字 `"/name args"` 放進 `session/prompt` 呼叫。來源：
  <https://agentclientprotocol.com/protocol/slash-commands>
- Config options：`configOptions[{id,name,type,currentValue,options,category?}]`，category
  含 `mode|model|model_config|thought_level`；`session/set_config_option{sessionId,configId,value}`
  回完整 `configOptions`；`config_option_update` 由 agent 推送；文件稱 modes API 將被取代。
  來源：<https://agentclientprotocol.com/protocol/session-config-options>
- Elicitation：schema 有 `elicitation/create`（`mode: form|url`，回應 `accept|decline|cancel`），
  client 需在 `initialize` 宣告 `clientCapabilities.elicitation`。穩定度 `unverified`
  （`/protocol/elicitation` 頁面 404，只在 schema 與 overview 看到）。
- Client capabilities（`initialize`）：`fs.{readTextFile,writeTextFile}`、`terminal`、
  `auth.terminal`、`elicitation`、`session.{configOptions.boolean,compaction,notices}`；
  全部 optional，省略即不支援。

### Adapter 與 agent 啟動

| Backend | 套件 / 指令 | 驗證 |
| --- | --- | --- |
| Claude Code | npm `@agentclientprotocol/claude-agent-acp`，`bin: claude-agent-acp`；`npm view` 於 2026-10-14：`latest = 0.90.0`、`preview = 0.90.1-preview.1`，repo `agentclientprotocol/claude-agent-acp`，Apache-2.0。舊名 `@zed-industries/claude-agent-acp` 最新 `0.23.1`，已標 deprecated（「renamed to @agentclientprotocol/claude-agent-acp」）。Lumen 預設啟動方式是 `exec.LookPath("claude-agent-acp")` 找全域安裝的 bin（見 Security disposition S-1），不用 `npx`；README 未寫 launch 指令，spike 實測：argv 只有 bin 路徑，不需任何旗標。來源：<https://github.com/agentclientprotocol/claude-agent-acp>、`npm view`（本機） | 套件名、bin、版本、啟動 verified（T2.0）；`authMethods` 回 `[]`（claude CLI 自行使用 keychain/login） |
| Gemini CLI | `gemini --acp`；`--experimental-acp` 已 deprecated（本機 `gemini 0.60.0` 的 `gemini --help` 顯示 `--acp  Starts the agent in ACP mode`、`--experimental-acp ... (deprecated, use --acp instead)`）。來源：<https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/acp-mode.md>；列出 `initialize/authenticate/newSession/loadSession/prompt/cancel`、`setSessionMode`、`unstable_setSessionModel` | verified（文件 + 本機 help） |
| Codex | 不走 ACP；沿用 `codex app-server`（`app.go:220` `startAppServer`、`app.go:243` `appServerArgs`） | verified（現況） |

Go SDK（非必要，見 ADR-4）：`github.com/coder/acp-go-sdk`，README 寫 `@v0.13.5`，Apache-2.0；它追蹤的
schema 版本、是否涵蓋 `session/list` / `session/resume` 在 repo 頁面都查不到，`unverified`。
來源：<https://github.com/coder/acp-go-sdk>

claude-agent-acp README 聲明支援：tool call + permission request、slash commands、client MCP
servers、`/mcp`、TODO list、nested subagent transcript、terminal；v2 surface 提到
`session/list`、`session/resume`、elicitation（URL elicitation 用於 MCP OAuth）。**T2.0 實測（0.90.0）：** `initialize` 回 `loadSession:true`；
`sessionCapabilities{list,resume,fork,close,delete,additionalDirectories}`；`promptCapabilities{image,embeddedContext}`；
`mcp{http,sse}`；`authMethods []`。`session/list` 回 `{sessions:[{sessionId,cwd,title,updatedAt}]}`，無 `nextCursor`
（仍依 `nextCursor` 分頁處理）；`session/load` 先以 `session/update` 重播歷史再回結果，`session/resume` 不重播。
`AskUserQuestion`：`clientCapabilities {}` 時被 adapter disallow；只有 client 宣告 `elicitation.form` 才以
`elicitation/create` 呈現（見 ADR-5、Open questions #3）。

## ADR

### ADR-1 以 ACP 當 plugin protocol

- **Decision：** Lumen 作為 ACP client，spawn agent subprocess，stdio JSON-RPC 2.0。
- **Reason：** 已有多個 agent 實作（Claude Code adapter、Gemini CLI），Lumen 不必為每個
  agent 維護 bespoke 解析；protocol 已涵蓋 streaming、tool call、permission、cancel、session
  list/load。
- **By：** Miyago（2026-10-14，已定，不重議）。
- **Consequence：** 每個 agent 獨有功能（fork-at-turn、goal、review 等）只能以 capability
  flag 表達，沒有就顯示 fallback。

### ADR-2 Codex 保留 native path

- **Decision：** `codexBackend` 包裝現有 `appServer`，不改走 ACP。
- **Reason：** fork-at-turn（`thread/fork.lastTurnId`）、goal、review、compact、完整 slash
  catalog、`token usage` 細項都是 codex app-server 專屬；ACP 沒有對等 method。
- **By：** Miyago（2026-10-14）。

### ADR-3 預設 backend = Claude Code

- **Decision：** 內建預設 `claude`，經 `claude-agent-acp`。
- **Consequence（breaking）：** 現有使用者執行 `lumen` 的預設行為從 Codex 變 Claude Code。
  釋出說明必須寫明，並提供 `LUMEN_BACKEND=codex` 或 config `default` 回復。`lumen codex ...` 與
  native subcommand passthrough（`nativeCodexInvocation`）不受影響。
- **By：** Miyago（2026-10-14）。

### ADR-4 傳輸層：自寫最小 ACP client，不引入 SDK（本 spec 的推薦，待核准）

- **Decision：** P2 重用 `appServer` 現有的 JSON-RPC plumbing 概念（`rpcMessage`、pending map、
  `readLoop`、`respond`），抽成 `jsonrpcConn`，ACP 只需約 12 個 method/notification 的手寫 struct。
- **Reason：** 現有依賴只有 `x/term`；SDK 的 schema 追蹤版本與 `session/list`/`resume` 覆蓋度查不到
  （unverified）；ACP v1 surface 小；自寫可直接共用 Lumen 既有的 panic recover、bounded channel 與
  reconnect 行為。
- **Reconsider if：** P2 實作超過 estimate 50%，或 schema 需要的欄位 > 手寫能維護的量。
- **這是我做的決定，不是 Miyago 已定；見 Open questions #1。**

### ADR-5 Client capabilities 第一個 slice：全部不宣告

- **Decision：** `initialize` 送 `clientCapabilities: {}`（不含 `fs`、`terminal`、`elicitation`），
  `clientInfo = {name:"lumen", title:"Lumen", version: appVersion}`。
- **Reason：**
  1. Claude Agent SDK 與 Gemini CLI 都自帶檔案與 shell 執行；沒有 client fs/terminal 它們用自己的
     （T2.0 已驗證：`clientCapabilities {}` 時 adapter 不報錯，仍能自行編輯檔案；`request_permission` 的 JSON-RPC `id` 為數字 `0`）。
  2. 實作 `fs/write_text_file` 或 `terminal/*` 等於 Lumen 要承擔 sandbox、路徑驗證與 process 管理，
     這是 safety 面的新增，不是 minimal。
  3. 少一個 inbound request 類別，UI 的 approval 路徑只剩 `session/request_permission`。
- **Fallback：** 若 spike 發現某 agent 缺 fs 就拒絕工作，最小補救順序：先加 `fs.readTextFile`
  （唯讀、限制在 session cwd 下的絕對路徑），再考慮 `writeTextFile`，`terminal` 最後。
- **不宣告 elicitation 的後果：** agent 無法對 Lumen 發 `elicitation/create`；Codex 的
  `item/tool/requestUserInput` 在 ACP backend 沒有對等路徑，capability `userInput=false`（見下）。
  T2.0 確認 Claude 的 `AskUserQuestion` 在未宣告 elicitation 時被 disallow（agent 不會呼叫）；第一個 slice 維持
  `clientCapabilities {}`，elicitation 為 follow-up（宣告 `elicitation.form`，映射到既有 `userInputQuestion` UI）。

## Architecture

### 分層

```text
ui / screen / dashboard / native_command_handlers   (只認 Backend + Event)
          │
   Backend interface  +  Capabilities  +  normalized Event
     ┌────┴───────────┐
codexBackend         acpBackend
(wraps appServer)    (jsonrpcConn + agent subprocess)
```

### Backend interface（草案，P1 定稿）

```go
// Backend 是 UI 唯一依賴的 seam。Events() 合併原本的 notifications / requests /
// errors / closed 四個 channel，UI event loop 只 select 一個。
type Backend interface {
    Name() string
    Caps() Capabilities
    Events() <-chan Event            // 關閉表示 backend 已結束；結束原因以 EvConnectionLost 先送出

    Open(ctx context.Context) error  // spawn + initialize (+ authenticate if needed)
    NewSession(cwd string, rt runtimeOverrides) (SessionInfo, error)
    ResumeSession(id string, cwd string, rt runtimeOverrides) (SessionInfo, error) // ACP: load 或 resume
    ListSessions(cwd string, limit int) ([]SessionInfo, error)                    // Caps.SessionList
    Prompt(sessionID, text string, rt runtimeOverrides) (turnID string, err error)
    Cancel(sessionID, turnID string) error
    ResolveApproval(reqID string, optionID string) error  // optionID == "" -> cancelled
    AnswerInput(reqID string, answers map[string][]string) error // Caps.UserInput
    Close()
}

// 只有 codexBackend 實作；UI 先檢查 Caps，再 type-assert。
type CodexExtras interface {
    ForkAt(id, lastTurnID string, rt runtimeOverrides) (SessionInfo, error)
    Compact(id string) error
    Review(id string, target map[string]any) (string, error)
    Goal(id string) / SetGoal / ClearGoal
    ListModels / ListSkills / ListHooks / ListMCPServers / ListApps / ListPlugins
    Archive / Delete / Rename
    Call(method string, params any, target any) error
}
```

- `SessionInfo`：`ID, Title, CWD, UpdatedAt, Model, ReasoningEffort, ApprovalPolicy, SandboxLabel,
  History []HistoryTurn`。`threadSummary` 變成 `codexBackend` 內部型別，轉成 `SessionInfo`。
  ACP 的 `Model` 等欄位在 `configOptions` 有 `category: model` 時填入，否則空（UI 顯示 `—`）。
- `Open` 與 `NewSession` 分開，讓 reconnect 可重用 `Open`。
- `Caps()` 在 `Open` 後才完整（ACP 的 capability 來自 `initialize` response）；UI 在 `Open` 後讀取。

### Capabilities

| Flag | codexBackend | acpBackend 來源 |
| --- | --- | --- |
| `SessionList` | true | `sessionCapabilities.list` |
| `SessionResume` | true | `loadSession` 或 `sessionCapabilities.resume` |
| `ReplayOnResume` | true（`Turns` 在 response） | `loadSession` 為 true（走 `session/update` 重播）；`resume` 為 false |
| `ForkAtTurn` | true | false |
| `Goal` | true | false |
| `Review` | true | false |
| `Compact` | true | false（`compaction_update` 只是顯示，不是觸發；agent 自己的 `/compact` 走 slash command） |
| `ModelList` | true（`model/list`） | `configOptions` 有 `category: model` |
| `ModelSwitch` | true（per-turn override） | 有 model config option 時 `session/set_config_option` |
| `SlashCatalog` | `static`（現行 catalog） | `dynamic`（`available_commands_update`） |
| `UserInput` | true | false（spike 後可能依 elicitation 改） |
| `TokenBreakdown` | true | false（只有 `used/size/cost`） |
| `NativeAdmin`（skills/hooks/mcp/apps/plugins） | true | false |
| `Rename/Archive/Delete` | true | `delete` 依 `sessionCapabilities.delete`；其餘 false |

### UI fallback 規則

- Capability 為 false 的 slash command 執行時，印出一行 muted status：
  `/<cmd> is not supported by backend <name>`（沿用 `printStatus`），**不得**把 slash text 當 prompt
  送給 model（延續 `docs/specs/lumen/SPEC.md` 的 catalog 規則）。
- Catalog：Lumen-local command（`/backend`、`/help`、`/exit`、`/copy`、`/diff`、`/pwd`、`/status`
  等不需 backend 的）永遠可用；codex catalog 其餘項目只在 `codexBackend` 顯示；ACP backend 的 popup
  顯示 Lumen-local + agent 的 `availableCommands`，執行時以 `"/name args"` 文字送 `session/prompt`。
- `[fork]` 點擊：`ForkAtTurn=false` 時不渲染按鈕（不是 disabled），避免死 affordance。
- Status/top bar 欄位缺資料就省略，不顯示假值（沿用 Hybrid UX 的 priority 壓縮規則）。

### Normalized Event model

UI 只處理下列 event（Go 以 sealed interface + struct 表達，P1 定稿）：

| Event | 欄位 |
| --- | --- |
| `EvTurnStarted` | `SessionID, TurnID` |
| `EvAssistantDelta` | `TurnID, Text` |
| `EvReasoningStarted` | `TurnID`（UI 設 `reasoningShown/reasoningActive`，顯示單一 thinking 狀態；`ui.go:1209-1221`） |
| `EvReasoningCompleted` | `TurnID`（UI 清 `reasoningActive`；`ui.go:1256-1257`） |
| `EvReasoningDelta` | `TurnID, Text`（可選；codex 目前不送 text delta，UI 預設不顯示 text，只更新 thinking 狀態） |
| `EvToolStarted` | `ToolID, Kind(command/edit/read/search/fetch/mcp/other), Title, Command?, Locations?` |
| `EvToolOutput` | `ToolID, Text` |
| `EvToolCompleted` | `ToolID, Status(ok/failed/cancelled), Label, ExitCode?, Diff?`；`Status` 只供邏輯判斷，`Label` 是 UI 逐字顯示的原始字串 |
| `EvPlanUpdated` | `Items[{Text, Status}]` |
| `EvSessionTitle` | `SessionID, Title` |
| `EvUsage` | `Used, Size?, Cost?, Breakdown?`（codex 填 breakdown，ACP 不填） |
| `EvTurnCompleted` | `TurnID, Status(completed/interrupted/failed/refused/maxTokens), Label, Err?`；`Label` 是 UI 逐字顯示的原始字串（取代 `"turn "+status`，`ui.go:1278`） |
| `EvNotice` | `Level(info/warn/error), Text` |
| `EvApprovalRequest` | `ReqID, TurnID, Title, Detail, Options[{ID, Label, Kind(allowOnce/allowAlways/rejectOnce/rejectAlways/cancel)}]` |
| `EvInputRequest` | `ReqID, TurnID, Questions[]`（維持現有 `userInputQuestion` 形狀） |
| `EvCommandsUpdated` | `Commands[{Name, Description, Hint?}]`（ACP only） |
| `EvConnectionLost` | `Err` |

ID 驗證（原 `validateServerRequest`）下沉到 backend：backend 只送屬於目前 session 的 event。

**Enum 與 Label 的分工：** `Status` enum 只用於 UI 邏輯分支（顏色、是否結束 turn、是否 retry）與 ACP 映射；
任何會出現在畫面或 transcript 的狀態文字一律用 `Label`。`codexBackend` 的 `Label` 逐字傳遞 codex 原值
（零文案變更，`lumen_test.go:1303-1325` 斷言 `failed exit 1`）；`acpBackend` 由 `stopReason` / tool status
產生 `Label`（例如 `cancelled`、`refused`）。

### 映射表

| Normalized | codex app-server（來源） | ACP |
| --- | --- | --- |
| `EvTurnStarted` | `turn/started`（`ui.go:987`） | client 自己在送出 `session/prompt` 時產生；`TurnID` = 本地遞增 id（ACP 沒有 turn id） |
| `EvAssistantDelta` | `item/agentMessage/delta`（`ui.go:1000`） | `session/update` `agent_message_chunk`（text content） |
| `EvReasoningStarted` | `item/started` 且 item type `reasoning`（`ui.go:1209-1221`；已顯示過則忽略，維持 `reasoningShown` 去重） | 該 turn 第一個 `agent_thought_chunk`（backend 以 per-turn flag 去重，只送一次） |
| `EvReasoningCompleted` | `item/completed` 且 item type `reasoning`（`ui.go:1256-1257`） | 該 turn 第一個非 thought 的 update（message / tool_call）或 turn 結束時補送一次 |
| `EvReasoningDelta` | 無（codex 無 text delta） | 每個 `agent_thought_chunk`；UI 預設不顯示 text |
| `EvToolStarted` | `item/started`（commandExecution / fileChange / mcpToolCall） | `tool_call`（`kind` 決定 command/edit/...；`kind=execute` -> command） |
| `EvToolOutput` | `item/commandExecution/outputDelta`（`ui.go:1021`） | `tool_call_update.content[]`（`content` text、`terminal` 因未宣告 terminal 不會出現） |
| `EvToolCompleted` | `item/completed`（`ui.go:1050`；`Label` 由 `codexBackend` 保留 `toolCompletionStatus(status, exitCode)`（如 `failed exit 1`）與 fileChange raw status 的現行字串，`ui.go:1234-1255`） | `tool_call_update.status` = `completed|failed`；cancel 時由 client 自己標 `cancelled` |
| `EvPlanUpdated` | `turn/plan/updated`（`ui.go:1052`） | `plan` |
| `EvSessionTitle` | `thread/name/updated`（`ui.go:1077`） | `session_info_update`（`title`、`updatedAt`；T2.0 實測欄位名） |
| `EvUsage` | `thread/tokenUsage/updated`（`ui.go:1085`，含 input/output/cache read/cache creation/context window） | `usage_update{used,size,cost}`；**缺口：** 無 input/output/cache 拆分，`/usage` 與 dock 的 breakdown 欄位省略，只顯示 `used/size` 與（如有）cost |
| `EvTurnCompleted` | `turn/completed`（`ui.go:1114`，`Label` = codex raw `status`，UI 仍顯示 `"turn "+Label`） | `session/prompt` response 的 `stopReason`：`end_turn`->completed、`cancelled`->interrupted、`refusal`->refused、`max_tokens`/`max_turn_requests`->maxTokens；JSON-RPC error->failed |
| `EvNotice` | `error`/`warning`/`configWarning`/`guardianWarning`（`ui.go:1124-1131`） | `notice`（需宣告 `session.notices`，P2 不宣告）；stderr 摘要不進 transcript |
| `EvApprovalRequest` | server request `item/commandExecution/requestApproval`、`item/fileChange/requestApproval`；`item/permissions/requestApproval` 維持現行「回空 grant」 | client method `session/request_permission`；`options[]` 一對一轉成 `Options`，`kind` 直接對應 |
| approval 回應 | `{"decision": accept|acceptForSession|decline|cancel}`（`ui.go:1470`） | `{outcome:"selected", optionId}`；使用者取消 -> `{outcome:"cancelled"}` |
| `EvInputRequest` | `item/tool/requestUserInput`（`ui.go:1356`） | 無對應（`UserInput=false`）；若日後宣告 elicitation，`elicitation/create` form 映射到 `Questions` |
| `EvCommandsUpdated` | 無（static catalog） | `available_commands_update` |
| Interrupt | `turn/interrupt`（`Interrupt(threadID, turnID)`，`app.go:737`） | `session/cancel` notification；見下 |
| New | `thread/start`（`app.go:388`） | `session/new{cwd, mcpServers: []}` |
| Resume | `thread/resume`（`app.go:412`，回完整 `Turns`） | `session/load`（重播）優先，其次 `session/resume`；兩者都無則 `SessionResume=false` |
| List | `thread/list`（`app.go:433`） | `session/list{cwd}`（需 capability），分頁用 `nextCursor` |
| Prompt | `turn/start`（`app.go:693`） | `session/prompt{prompt:[{type:"text",text}]}`（長時間 blocking request，response 即 turn 結束） |

### Interrupt / Cancel 語意（ACP）

1. UI 呼叫 `Cancel`：先把所有 pending approval 以 `{outcome:"cancelled"}` 回覆（spec MUST），再送
   `session/cancel`，並把本 turn 未完成的 tool 在 UI 標 `cancelled`。
2. 繼續接受 cancel 之後到達的 `session/update`，直到 `session/prompt` response（`stopReason:"cancelled"`）
   才送 `EvTurnCompleted{interrupted}`。
3. Cancel 後 N 秒（預設 10，常數，不開放設定）仍無 response 的處理：顯示 notice
   「agent not responding to cancel」，composer 解鎖由使用者決定是否 `/backend` 重啟。

### Resume / session list

- 啟動：`--resume <id>` / `--continue` 對選定 backend 生效；ACP 的 `--continue` 需要 `SessionList`，
  否則報錯「backend X cannot list sessions」，不默默開新 session。
- `session/load` 重播 `user_message_chunk` / `agent_message_chunk` / `tool_call` 等進 transcript：
  使用者主動 `/resume` 時清空 transcript 後重播；**reconnect** 時 transcript 已存在，重播 event
  要丟棄（`replaying=true` 旗標），避免重複。
- 若 `loadSession=false` 且無 `resume`：`/resume` 與 `--resume` 顯示 fallback，session picker 不顯示。

### Reconnect（`session_supervisor.go` / `lifecycle.go`）

- `sessionSupervisor.factory` 改為 `func() (Backend, error)`；`reconnectResult.client` 改為
  `Backend`；`reconnectWithSnapshot` 流程改為 `Open` -> `ResumeSession(snapshot.id)` ->（失敗且
  transcript 有內容）`NewSession`，語意與現況（`canStartFreshThreadAfterResume`）一致。
- bounded backoff（`initialReconnectDelay=250ms`、`maxReconnectDelay=4s`）、重連期間畫面保持可見、
  失敗顯示狀態不 busy-loop：全部沿用。
- ACP 差異：agent subprocess 結束（EOF、非零 exit）視為 `EvConnectionLost`；重連後用
  `session/load`（或 `resume`）接回同一 `sessionId`；兩者皆無則 `NewSession` 並印 notice
  「agent context lost, started a new session」。進行中 turn 一律視為 interrupted。
- Codex：行為不變。

### Credentials 與 logging

- Lumen 不讀、不存、不轉送任何 API key。agent subprocess 繼承 Lumen 的環境變數（與現在
  `codex app-server` 一致）；config 不支援 `env` 欄位（見 S-2），`args` 不得含 secret（文件明寫，並建議用
  `agent-secret run <name> -- lumen ...` 注入）。
- command/args 在任何 error、status、log 輸出前只顯示 backend name 與 command basename，不印完整
  args 與 env。
- P2 不寫 protocol wire log。若日後加 debug trace，必須 redact `env`、`headers`、`authorization`
  欄位與所有 `authenticate` params。
- `authenticate`：agent 在 `initialize` 回非空 `authMethods` 或 `session/new` 回 auth 錯誤時，
  Lumen 顯示「backend X requires login: run `<其登入指令>` in another terminal」並停在
  disconnected 狀態，不在 TUI 內處理 credential（`auth.terminal` 不宣告）。Claude adapter 實測 `authMethods` 為 `[]`
  （claude CLI 自行使用 keychain/login）；登入指令來源 `unverified`。

## Backend 選擇與 `/backend`

### Config（最小）

內建預設（編譯進 binary，零設定可用）：

```text
claude : acp   claude-agent-acp            （exec.LookPath；找不到時顯示 `npm i -g @agentclientprotocol/claude-agent-acp@0.90.0` 並停在 disconnected，不自動下載）
gemini : acp   gemini --acp
codex  : codex native (codex app-server)
```

可選檔案 `${XDG_CONFIG_HOME:-~/.config}/lumen/backends.json`（stdlib `encoding/json`，無新依賴）：

```json
{
  "default": "claude",
  "backends": {
    "claude": { "type": "acp", "command": "claude-agent-acp" },
    "gemini": { "type": "acp", "command": "gemini", "args": ["--acp"] },
    "codex":  { "type": "codex" }
  }
}
```

- 檔案內同名 entry 覆蓋內建；新增 entry 即新增 backend。`type` ∈ `acp | codex`；`acp` 需
  `command`，`args` 選填。
- Claude 預設不用 `npx`：`npx -y` 會在啟動時直接下載並執行 registry 上的程式碼。預設只走
  `exec.LookPath("claude-agent-acp")`；安裝指令釘在已驗證版本 `npm i -g @agentclientprotocol/claude-agent-acp@0.90.0`
  （不用 `@latest` / `@preview`）。`npx` 只能由使用者在 `backends.json` 明確 opt-in，且 `args` 必須含 pinned 版本；
  升版是文件中的明確一行變更。
- `backends.json` 的讀取與信任規則見 Security disposition S-2；config 不支援 `env`。
- 這是 Lumen 第一個設定檔；`docs/specs/lumen/SPEC.md` 的 welcome-card 條款「不得新增設定來源」
  只針對 welcome card，不衝突，但需在 README 註明。

### 選擇優先序

`--backend <name>` > `LUMEN_BACKEND` > config `default` > 內建 `claude`。未知 name 時列出可用 backend
並以 exit code 2 結束。

### 旗標相容性

`--model`、`--reasoning-effort`、`--sandbox`、`--approval-policy` 目前是 codex 語意。非 codex backend
收到這些旗標時**明確報錯**（`flag --sandbox is only supported by backend codex`），不默默忽略。
ACP 的 model 切換之後再以 `session/set_config_option` 提供（P3 之後，非本 spec 必要）。

### `/backend`

- `/backend`（無參數）：開 picker，列出已設定 backend、目前者標記、各自 capability 摘要。
- `/backend <name>`：切換。
- 語意：
  1. busy turn、pending approval、pending input 或 queued prompt 存在時拒絕，提示先 interrupt。
  2. 先 `Open` + `NewSession`（同 cwd、不帶 codex runtime overrides）新 backend；**成功後**才 `Close` 舊
     backend 並 swap；失敗則保留舊 backend 並顯示錯誤（atomic swap）。
  3. 切換 = 新 session。舊 transcript 保留在畫面，插入一行分隔 status：
     `backend: codex → claude · new session`；composer history 保留；不轉移上下文給新 backend。
  4. 切換後重算 slash catalog、dock、capability-gated affordance。
  5. 不改 config 檔；只影響目前 process。

## Phased plan 與估時

估時為單人專注工時，不含等待 review。

| Phase | 內容 | 估時 | 完成條件 |
| --- | --- | --- | --- |
| P1 | UI 搬到 normalized events；`codexBackend` 包 `appServer`；定義 `Backend`/`Capabilities`/`Event`；`session_supervisor`/`lifecycle`/`screen`/`dashboard`/`native_command_handlers` 改吃 interface。**零行為變更** | 2.5–3.5 天 | 既有 `go test -race ./...` 全綠、無 golden/文案變更、手動 smoke 與 P1 前一致 |
| P2 | `acpBackend` MVP：spike（0.5 天，實跑 claude adapter 與 `gemini --acp` 抓 `initialize` response，確認 capability/auth/無 fs 行為）、`jsonrpcConn`、`initialize`、`session/new`、`load`/`resume`、`prompt`+streaming、tool call、`request_permission`、`cancel`；fake ACP agent test fixture | 3.5–5 天 | fake agent 全套 EARS 通過；真實 claude adapter 手動 smoke 一個 prompt + 一個 approval + 一個 cancel |
| P3 | 內建 registry + config 檔、`--backend`、`LUMEN_BACKEND`、預設改 `claude`、`/backend` picker 與切換、旗標相容性錯誤、capability fallback 文案 | 1.5–2 天 | 對應 EARS 通過；`LUMEN_BACKEND=codex` 回復原行為 |
| P4 | README、help、install notes、breaking-change 說明、`docs/specs/lumen/SPEC.md` 加交叉參照、版本號 bump 建議 | 0.5 天 | 文件與 `--help` 一致 |
| 合計 | | 約 8–11 天 | |

P1 的 refactor 是風險最大的一步（`ui.go` 2023 行、`lumen_test.go` 2254 行、測試目前有直接餵 codex
notification 的 fixture）；建議 P1 內部再拆：(a) 先引入 `Event` 與 codex->Event 轉譯函式，UI 仍可編譯；
(b) UI 切換到 `Event`；(c) 抽 `Backend` interface。每步 `go test -race ./...` 綠才進下一步。

## Security disposition

來源：security-reviewer findings（2026-10-14）。參考：
[claude-agent-acp](https://github.com/agentclientprotocol/claude-agent-acp)、
[block/buzz#2884](https://github.com/block/buzz/issues/2884)、
[Claude Agent SDK permissions](https://code.claude.com/docs/en/agent-sdk/permissions)。
T2.0 spike 已實測（2026-10-14，`claude-agent-acp@0.90.0`）；仍標 `unverified` 的是 spike 未涵蓋的項目。

### S-1 Adapter 啟動（Severity M）

- 預設用 `exec.LookPath("claude-agent-acp")` 找全域安裝的 bin。找不到時顯示
  `npm i -g @agentclientprotocol/claude-agent-acp@0.90.0`，backend 停在 disconnected，**不自動下載**。
- `npx` 只作為使用者在 `backends.json` 明確 opt-in 的 command；內建 registry 不含 `npx`。
- ACP subprocess 的 `cmd.Dir` 固定為 Lumen 自有目錄（`${XDG_STATE_HOME:-~/.local/state}/lumen`，不存在時以 0700
  建立），**不是**專案 cwd。專案路徑只透過 `session/new.cwd`（與 `session/load.cwd`）傳遞。
  理由：避免專案目錄內的檔案（`.npmrc`、`node_modules/.bin`、專案層 config）影響 adapter 啟動。
- Spike 結果（T2.0）：
  - (a) adapter **不依賴** process cwd：`cmd.Dir` 為空目錄、`session/new.cwd` 指專案時，檔案寫入專案、專案 settings
    被讀取；`process.cwd()` 只用於 auth probe fallback。`session/list` 若不帶 `cwd` 會回傳**所有**專案的 session
    （實測 244 筆）-> **Lumen 的 `session/list` 一律帶 `cwd`**。
  - (b) 0.90.0 **沒有** `npm-shrinkwrap.json` 或 lockfile；4 個直接依賴 pin 版，間接依賴未鎖。故維持 pin
    `@0.90.0` + 全域安裝（不自動下載）。
- **Subprocess env（決定）：** 啟動 ACP agent 前，Lumen 從繼承的環境移除 `CLAUDE_CODE_EXECUTABLE` 與 `ANTHROPIC_*`
  （實測 adapter 會把完整 env 傳給 claude CLI child，`CLAUDE_CODE_EXECUTABLE` 可改變實際執行的 binary）。其餘照常
  繼承（`HOME`/`PATH`/`USER`/`TMPDIR`/`LANG`/`TERM` 等），claude CLI 自己的 keychain/login 認證不受影響。實測 adapter
  收到的 env key 只有 `PATH,HOME,USER,TMPDIR,LANG,TERM`，argv 只有 bin 路徑，Lumen 不傳任何 bypass flag/env。
  adapter 本身啟動 claude CLI 時會帶 `--allow-dangerously-skip-permissions`（只是讓 bypass「可被選取」，非啟用）。

### S-2 `backends.json`（Severity L）

- 只讀 `${XDG_CONFIG_HOME:-~/.config}/lumen/backends.json`；**絕不**讀 cwd 或專案內任何 config。
- 檔案 owner 不是目前 uid，或 group/world 可寫 -> 以 exit code 2 結束，訊息點名該檔案，且不啟動任何 backend。
- 不支援 `env`（遇到該 key 視為設定錯誤，exit 2）；欄位白名單：`default`、`backends.<name>.{type,command,args}`，
  其他 key 一律拒絕。
- ACP agent 的 stderr 進有界 ring buffer（行數與單行長度都有上限），**不寫到 terminal 或檔案**；只在
  `EvConnectionLost` 時顯示最後 N 行。
- Follow-up（不在本 spec 範圍）：`app.go:226` 的 codex 路徑目前設 `cmd.Stderr = os.Stderr`，與上述原則不一致；
  改動小時順手處理，否則另開 issue。

### S-3 Permission 路由（Severity M）

- `session/request_permission` 的回覆 `optionId` 只能取自該 request 的 `options[]`，Lumen 不自造 id。
- 以下情況一律回 `{outcome:"cancelled"}`：無 TTY / non-interactive、timeout、`sessionId` 與目前 session 不符、
  UI 無法 render 該 request。
- 只有使用者明確按鍵才能回 `selected`；approval card 預設 focus **不得**落在任何 `allow_*` option。
  （反例：buzz-acp 對每個 request 自動回 `allow_once`，使 `default`/`acceptEdits` 實質等同
  `bypassPermissions`，見 buzz#2884。）
- Lumen **不送**任何會改變 mode/permission 的 request（`session/set_mode`、對 mode/permission 類
  `session/set_config_option`），也不傳 bypass flag 或 env。
- 風險（機制見官方文件，T2.0 已對 adapter 實測，結果見下）：Agent SDK 預設 `query()` 會讀取專案
  `.claude/settings.json` 的 allow/deny/ask rules 與 `permissions.defaultMode`，session 起始 mode 可能是
  `auto`（[permissions 文件](https://code.claude.com/docs/en/agent-sdk/permissions)）；若 `claude-agent-acp@0.90.0`
  沿用這些行為，則 user/project settings 可能在 Lumen 不知情下放行工具或啟用 bypass。
- 緩解：Lumen 讀取 `session/new` 回傳的 mode 與後續 `current_mode_update`；mode 不是 `default` 時，status bar
  **持續**顯示 `permissions: <mode> (from agent settings)`，直到 mode 回到 `default`。
- Spike 結果（T2.0）：
  - (c) `settingSources` 在 0.90.0 寫死為 `["user","project","local"]`，可被 `session/new._meta.claudeCode.options`
    覆寫，但**初始 mode 不受該覆寫影響**。Miyago 的 user settings `defaultMode: auto` -> `session/new` 回 `auto`；
    project settings：`plan` 生效，`acceptEdits`/`bypassPermissions` 退回 `default`；settings.local：`acceptEdits` 生效，
    `bypassPermissions` 退回 `default`（原因未明）。project `allow:["Write"]` **沒有**跳過 `request_permission`。
    `unverified`：user-level allow rules 是否跳過 prompt。
  - (d) mode 讀取：`result.modes.currentModeId`，或 `result.configOptions[id=mode].currentValue`。`availableModes` =
    `default, acceptEdits, plan, auto, bypassPermissions`。`current_mode_update` 在 runtime 未觀察到（僅見 adapter 原始碼
    `session-mode.js:170`），Lumen 仍須處理。
  - (e) Lumen client 只送：`initialize`、`session/new`、`session/prompt`、`session/list`、`session/load`、`session/resume`、
    `session/cancel`；不送 `session/set_mode`、`session/set_config_option`、`authenticate`。
- **決定（限縮 bypass）：** 對 claude backend，Lumen 在 `session/new`（及 `load`/`resume`）送
  `_meta.claudeCode.options.allowDangerouslySkipPermissions:false`。這只是限制（把 `bypassPermissions` 從
  `availableModes` 移除），**不是**要求變更 mode；不違反「不送 mode-changing request」。
- **決定（mode 警示）：** status bar 警示讀取 `session/new` 的 `result.modes.currentModeId`，或
  `configOptions[id=mode].currentValue`，並處理 `current_mode_update`；非 `default` 時持續顯示
  `permissions: <mode> (from agent settings)`。
- **決定（JSON-RPC id）：** JSON-RPC 層必須接受數值 `id: 0` 的 server request（`request_permission` 實測 id 為 `0`），
  不可把 0 當成「無 id」。
- **決定（AskUserQuestion）：** 第一個 slice 送 `clientCapabilities {}`，使 `AskUserQuestion` 被 adapter disallow；
  elicitation 為 follow-up，不在 P2。
- 實測 permission 行為：`request_permission` options = `allow-once (allow_once)`、`allow-with-updates (allow_always)`、
  `reject (reject_once)`。回 `cancelled` -> tool failed、turn 繼續並以 `end_turn` 結束；`allow-once` -> 檔案寫入。
  `session/cancel` -> `stopReason: cancelled` 約 8 ms，cancel 後仍會到數個 chunk。

## Risks

| Risk | 影響 | 緩解 |
| --- | --- | --- |
| P1 refactor 回歸 | 高 | 三步拆分、現有測試當 safety net、不改文案；既有測試改為餵 normalized event 的同時保留一組 codex 轉譯 golden test |
| Claude adapter 的 v1 surface 缺 `session/list`/`load`/`resume`（README 只在 v2 surface 提到） | 中 | P2 spike 前置；缺就以 capability flag 降級，`--continue`/`/resume` 顯示 fallback |
| 未宣告 fs/terminal 時 adapter 行為未知 | 中 | spike 驗證；有退路（ADR-5 fallback） |
| 全域 bin 未安裝導致預設 claude 無法啟動 | 中 | 明確顯示安裝指令並停在 disconnected（S-1）；不自動下載；啟動時顯示 connecting 狀態與 timeout（建議 30 秒，常數） |
| 預設改 Claude 是 breaking change | 中 | release note、`LUMEN_BACKEND=codex`；P4 文件明寫 |
| ACP 沒有 turn id、沒有 token 拆分、沒有 fork/goal/review | 低 | capability flag + fallback；已列於映射表 |
| ACP schema 演進（v2 alpha 在進行） | 低 | 只對 `protocolVersion: 1`；unknown `sessionUpdate` 忽略並計數，不 crash |
| Permission 語意差異（`allow_always` 範圍由 agent 定義） | 低 | UI 顯示 agent 給的 `name`，不自創說明 |
| 多 backend 後 session id 空間不同 | 低 | session id 一律綁定 backend name，picker 只列目前 backend 的 session |

## Open questions

1. ADR-4（自寫 vs `coder/acp-go-sdk`）：我推薦自寫，Miyago 若偏好 SDK 請在 P2 前說，會改 `jsonrpcConn` 為 SDK
   wrapper。
2. （已由 security review 決定）預設 command 為全域安裝的 `claude-agent-acp`，`npx` 只能明確 opt-in；見
   Security disposition S-1。
3. （T2.0 已回答）Claude 的互動式提問以 `elicitation/create` 呈現，且只在 client 宣告 `elicitation.form` 時；P2 不宣告，
   `AskUserQuestion` 被 disallow；elicitation 為 follow-up。live elicitation 行為 `unverified`。
4. `/backend` 切換時是否要自動把舊 transcript 摘要當第一個 prompt 帶給新 backend？本 spec 預設「不帶」。
5. 非 codex backend 的 `--model` 等旗標要不要在 P3 就支援（經 `session/set_config_option`）？本 spec 預設
   「報錯」。
6. Gemini 的 ACP 認證流程與 `gemini --acp` 的實際 capability `unverified`：spike 時 gemini 0.60.0 因 `IneligibleTierError`
   （無 API key）60 秒內未回應 `initialize`；原始碼顯示 `loadSession`、image/audio/embeddedContext、4 種 auth methods。
7. config 檔格式 JSON 是否可接受（避免新依賴）？若偏好 TOML 需評估新增依賴。
8. （T2.0 已回答大部分，見 S-1/S-3）仍 `unverified`：user-level allow rules 是否跳過 prompt；project `allow:["Write"]` 為何沒跳過；
   settings.local `bypassPermissions` 退回 `default` 的原因；`current_mode_update` 的 runtime 行為；live elicitation；
   gemini runtime；pending permission 時 `session/cancel` 的行為。

## Files（預計變更，P1–P4）

- 新增：`internal/lumen/backend.go`（interface、Capabilities、Event）、`backend_codex.go`、
  `backend_acp.go`、`jsonrpc.go`、`backend_config.go`、`backend_cmd.go`（`/backend`）、
  `testdata/fakeacp/`（fake agent）
- 修改：`ui.go`、`screen.go`、`lifecycle.go`、`session_supervisor.go`、`dashboard.go`、
  `native_command_handlers.go`、`native_commands.go`、`app.go`（`Run`、`parseOptions`）
- 文件：`README.md`、`docs/specs/lumen/SPEC.md`（交叉參照）
- 不改：`cmd/lumen/main.go`、`install.sh`、`uninstall.sh`

## Sources

- <https://agentclientprotocol.com/protocol/overview>
- <https://agentclientprotocol.com/protocol/initialization>
- <https://agentclientprotocol.com/protocol/session-setup>
- <https://agentclientprotocol.com/protocol/session-list>
- <https://agentclientprotocol.com/protocol/prompt-turn>
- <https://agentclientprotocol.com/protocol/tool-calls>
- <https://agentclientprotocol.com/protocol/slash-commands>
- <https://agentclientprotocol.com/protocol/session-config-options>
- <https://agentclientprotocol.com/protocol/schema>
- <https://github.com/agentclientprotocol/agent-client-protocol/releases>
- <https://github.com/agentclientprotocol/claude-agent-acp>（+ `npm view`，2026-10-14）
- <https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/acp-mode.md>（+ 本機 `gemini --help`，0.60.0）
- <https://github.com/coder/acp-go-sdk>
