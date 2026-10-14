---
id: tests-lumen-backend-adapter
title: Lumen pluggable backend - acceptance tests
status: draft
created: 2026-10-14
updated: 2026-10-14
spec: spec-lumen-backend-adapter
---

<!-- markdownlint-disable MD025 -->
# Acceptance tests (EARS)

<!-- markdownlint-enable MD025 -->

EARS 格式：`WHEN <trigger> THE SYSTEM SHALL <response>`；`WHILE <state> ...`；`IF <condition> THEN ...`；
`THE SYSTEM SHALL ...`（ubiquitous）。Kind：U = unit、I = integration（fake ACP agent / fake codex）、
M = 手動 smoke。所有 U/I 以 `go test -race ./...` 執行。

## P1：normalized events（codexBackend）

- **T-P1-01 (U)** THE SYSTEM SHALL 提供 `Backend` interface、`Capabilities` 與 `Event` 型別，且 `ui`、
  `screen`、`dashboard`、`native_command_handlers` 不再 import 或引用 codex method name 字串
  （以 `grep -rn '"item/\|"turn/\|"thread/' internal/lumen --include=*.go` 排除 `app.go`（`appServer` 留在該檔）、
  `backend_codex*.go` 與 `*_test.go` 後無結果驗證）。
- **T-P1-02 (U)** WHEN codex 送出 `turn/started`、`item/started`（`commandExecution`、`fileChange`、
  `mcpToolCall`、`reasoning` 四種 item type）、`item/agentMessage/delta`、`item/commandExecution/outputDelta`、
  `item/completed`（`commandExecution`、`fileChange`、`reasoning`）、`turn/plan/updated`、
  `thread/name/updated`、`thread/tokenUsage/updated`、`turn/completed`、`error`、`warning`、`configWarning`、
  `guardianWarning` THE SYSTEM SHALL 轉成對應 `Event`，且 payload 欄位與轉換前 UI 所用的值相同；`Label`
  逐字等於 codex 原值（含 `failed exit 1`、fileChange raw status、`turn completed/interrupted/failed`）
  （golden table test，case 數必須 >= `ui.go:987-1356` 內所有 `case` 分支，P1 以腳本比對 case 清單防漏）。
- **T-P1-03 (U)** WHEN codex 送出三種 requestApproval 或 `item/tool/requestUserInput` THE SYSTEM SHALL
  轉成 `EvApprovalRequest` / `EvInputRequest`，`availableDecisions` 成為 `Options`；thread/turn id 不符時
  不送出 event 並回覆 codex error（保留 `validateServerRequest` 行為）。
- **T-P1-04 (I)** WHEN fake codex backend 依序送出 turn started、delta、tool、completed THE SYSTEM SHALL
  產生與重構前相同的 transcript 與 screen model。
- **T-P1-05 (U)** WHEN 使用者在 approval card 選 accept / acceptForSession / decline / cancel THE SYSTEM
  SHALL 對 codex 回 `{"decision": ...}`，且只接受 `availableDecisions` 內的值。
- **T-P1-06 (U)** WHEN backend 的 capability 為 false 且使用者執行對應 slash command THE SYSTEM SHALL 顯示
  `/<cmd> is not supported by backend <name>` 且不把 slash text 送成 prompt；`codexBackend` 下所有現有
  command 行為不變。
- **T-P1-07 (I)** WHEN codex 連線中斷 THE SYSTEM SHALL 以 bounded backoff 重啟並 `ResumeSession`，
  resume 失敗（`no rollout found`）且 transcript 為空時開新 session；transcript 有內容時保留原錯誤、不開新
  session（與重構前的 supervisor 測試及 `canStartFreshThreadAfterResume` 等價）。
- **T-P1-08 (U)** THE SYSTEM SHALL 讓重構前既有的 `go test -race ./...` 全部通過；測試改動只限於輸入改成
  normalized event，無新增 skip。
- **T-P1-09 (M)** WHEN 以預設方式（P1 尚無 `--backend`）啟動並走 prompt / approval / interrupt /
  fork / `/goal` / `/compact` / reconnect THE SYSTEM SHALL 行為與 P1 前一致。
- **T-P1-10 (U)** WHEN codex 送出 reasoning item started / completed THE SYSTEM SHALL 產生
  `EvReasoningStarted` / `EvReasoningCompleted`，UI 的 `reasoningShown` / `reasoningActive` 狀態轉換與
  `lumen_test.go:1286-1300` 的現有斷言一致；重複的 started 不得重複顯示 thinking row。

## P2：acpBackend

- **T-P2-01 (U)** THE SYSTEM SHALL 以共用 JSON-RPC conn 同時服務 `appServer` 與 `acpBackend`；
  malformed JSON 行不得使 read loop 結束；單一 handler panic 不得結束 session。
- **T-P2-02 (I)** WHEN `acpBackend.Open` THE SYSTEM SHALL 送 `initialize` 且
  `protocolVersion = 1`、`clientCapabilities = {}`、`clientInfo.name = "lumen"`。
- **T-P2-03 (I)** IF agent 回 `protocolVersion` 不是 1 THEN THE SYSTEM SHALL 關閉 subprocess 並顯示版本不符
  錯誤，不進入 session。
- **T-P2-04 (I)** WHEN `NewSession` THE SYSTEM SHALL 送 `session/new` 且 `cwd` 為絕對路徑、`mcpServers`
  為空陣列。
- **T-P2-05 (I)** WHEN agent 宣告 `loadSession` 且使用者 resume THE SYSTEM SHALL 送 `session/load`
  並把重播 update 渲染成 transcript；IF 只有 `sessionCapabilities.resume` THEN 送 `session/resume` 且不重播；
  IF 兩者皆無 THEN `SessionResume=false` 並顯示 fallback；IF 有 `sessionCapabilities.list` THEN `session/list`
  依 `nextCursor` 分頁取完，否則不得呼叫 `session/list`。
- **T-P2-06 (I)** WHEN agent 送 `agent_message_chunk` THE SYSTEM SHALL 逐 chunk 產生 `EvAssistantDelta`；
  `agent_thought_chunk` -> 第一個送 `EvReasoningStarted`（per-turn 只一次）後接 `EvReasoningDelta`；
  遇到第一個非 thought update 或 turn 結束時送 `EvReasoningCompleted`。
- **T-P2-07 (I)** WHEN agent 送 `tool_call` 與一連串 `tool_call_update` THE SYSTEM SHALL 產生
  `EvToolStarted` / `EvToolOutput` / `EvToolCompleted`，只含變動欄位的 update 不得清掉先前欄位；
  `status=failed` -> `Status=failed`。
- **T-P2-08 (I)** WHEN agent 送 `plan`、`usage_update`、`available_commands_update`、`session_info_update`
  THE SYSTEM SHALL 分別更新 plan、`used/size`（及 `cost`）、動態 slash catalog、session title；IF 收到未知
  `sessionUpdate` THEN 忽略並計數，session 不中止。
- **T-P2-09 (I)** WHEN agent 呼叫 `session/request_permission` THE SYSTEM SHALL 顯示 approval card，
  options 依序對應 `allow_once / allow_always / reject_once / reject_always` 與 agent 提供的 `name`，並回
  `{outcome:"selected", optionId}`；使用者取消 -> `{outcome:"cancelled"}`。
- **T-P2-10 (I)** WHEN 使用者在 busy turn 按 interrupt THE SYSTEM SHALL 先把所有 pending permission 回
  `cancelled`，再送 `session/cancel` notification，並把未完成 tool 標 cancelled；WHILE 等待 `session/prompt`
  response THE SYSTEM SHALL 仍接受 cancel 後到達的 update；WHEN 收到 `stopReason:"cancelled"` THE SYSTEM SHALL
  結束 turn 為 interrupted；IF 10 秒內無 response THEN 顯示 `agent not responding to cancel` notice。
- **T-P2-11 (U)** THE SYSTEM SHALL 提供 fake ACP agent fixture，可腳本化 happy path、permission、cancel、
  crash、load replay、auth required、unknown update、慢回應，且 fixture 不依賴網路或真實 credentials。
- **T-P2-12 (I)** WHEN ACP subprocess 非預期結束 THE SYSTEM SHALL 產生 `EvConnectionLost`，以 bounded
  backoff 重啟，並依 `load` -> `resume` -> `NewSession`（附 notice）順序接回；重播 update 在 reconnect 時不得
  重複寫入既有 transcript；進行中 turn 視為 interrupted。
- **T-P2-13 (I)** IF `initialize` 回非空 `authMethods` 或 `session/new` 回 auth 錯誤 THEN THE SYSTEM SHALL
  顯示 `backend <name> requires login` 並停在 disconnected，不嘗試於 TUI 內收集 credential。
- **T-P2-14 (I)** WHEN `session/prompt` 回 `stopReason` 為 `end_turn / cancelled / refusal / max_tokens /
  max_turn_requests` 或 JSON-RPC error THE SYSTEM SHALL 分別映射為 completed / interrupted / refused /
  maxTokens / maxTokens / failed。
- **T-P2-15 (U)** THE SYSTEM SHALL NOT 在任何 status、error、log 輸出中包含 backend 的完整 `args` 或 `env`
  值（以含假 secret 的 fake command 驗證輸出不含該字串）。
- **T-P2-16 (M)** WHEN 以真實 `claude-agent-acp` 跑 prompt、approval、cancel、kill agent process THE SYSTEM
  SHALL 行為符合上述；結果與 adapter 版本記錄於 `PROGRESS.md`。
- **T-P2-17 (I)** WHEN starting an ACP backend THE SYSTEM SHALL set the subprocess working directory to a
  Lumen-owned fixed directory (`${XDG_STATE_HOME:-~/.local/state}/lumen`), not the project cwd; the project path
  SHALL be passed only via `session/new.cwd` / `session/load.cwd`（fake agent 回報自己的 `os.Getwd()` 驗證）。
- **T-P2-18 (I)** IF the default `claude-agent-acp` binary is not found by `exec.LookPath` THEN THE SYSTEM SHALL
  show `npm i -g @agentclientprotocol/claude-agent-acp@0.90.0` and stay disconnected; THE SYSTEM SHALL NOT
  auto-download or invoke `npx` unless the user's `backends.json` explicitly configures it。
- **T-P2-19 (I)** THE SYSTEM SHALL NOT write agent stderr directly to the terminal or to a file; agent stderr
  SHALL go to a bounded ring buffer and only the last N lines SHALL be shown on connection lost（fake agent 灌入大量
  stderr，驗證不出現在 terminal 輸出、記憶體有上限）。
- **T-P2-20 (I)** THE SYSTEM SHALL reply to `session/request_permission` with `outcome:"selected"` only after an
  explicit user keypress, and only with an `optionId` present in that request's `options[]`; IF there is no TTY,
  the request times out, the request `sessionId` does not match the current session, or the UI cannot render the
  request THEN THE SYSTEM SHALL reply `{outcome:"cancelled"}`。
- **T-P2-21 (U)** WHILE an approval card is shown THE SYSTEM SHALL NOT place default focus on any `allow_once` or
  `allow_always` option。
- **T-P2-22 (I)** WHEN `session/new` returns a mode other than `default`, or a `current_mode_update` changes the mode
  to a non-default value, THE SYSTEM SHALL persistently show `permissions: <mode> (from agent settings)` in the
  status bar until the mode returns to `default`。
- **T-P2-23 (I)** THE SYSTEM SHALL NOT send `session/set_mode`, a mode/permission `session/set_config_option`, or
  any other permission-mode-changing request, and SHALL NOT pass a bypass flag or env to the agent
  （fake agent 記錄收到的所有 request 與 argv/env，斷言無此類內容）。

## P3：選擇與切換

- **T-P3-01 (U)** THE SYSTEM SHALL 內建 `claude`（acp，command 為 `claude-agent-acp`，不含 `npx`）、`gemini`（acp）、`codex`（codex）三個 backend，
  無設定檔時可用。
- **T-P3-02 (U)** WHEN `backends.json` 存在 THE SYSTEM SHALL 以同名 entry 覆蓋內建並接受新增 entry；IF `type`
  不是 `acp|codex` 或 `acp` 缺 `command` THEN 以明確錯誤結束（exit code 2）。
- **T-P3-03 (U)** THE SYSTEM SHALL 依 `--backend` > `LUMEN_BACKEND` > config `default` > `claude` 的順序決定
  backend；IF name 未知 THEN 列出可用 backend 並以 exit code 2 結束。
- **T-P3-04 (U)** IF backend 不是 `codex` 且帶 `--model / --reasoning-effort / --sandbox / --approval-policy`
  THEN THE SYSTEM SHALL 以 exit code 2 並指名該旗標與 backend，不得默默忽略。
- **T-P3-05 (U)** WHEN `lumen codex ...` 或 native subcommand 被呼叫 THE SYSTEM SHALL 與本變更前一樣直接
  passthrough 給官方 `codex` binary（`nativeCodexInvocation` 不受 backend 預設影響）。
- **T-P3-06 (I)** WHEN 使用者 `/backend <name>` 且 session idle THE SYSTEM SHALL 先 `Open` + `NewSession`
  新 backend，成功後才關閉舊 backend；舊 transcript 保留並插入 `backend: <old> → <new> · new session`。
- **T-P3-07 (I)** IF 新 backend 啟動或 `NewSession` 失敗 THEN THE SYSTEM SHALL 保留舊 backend 與目前
  session 並顯示錯誤。
- **T-P3-08 (U)** IF busy turn、pending approval、pending input 或 queued prompt 存在 THEN `/backend`
  SHALL 拒絕並提示先 interrupt。
- **T-P3-09 (U)** WHILE 目前 backend 的 `ForkAtTurn=false` THE SYSTEM SHALL NOT 渲染 `[fork]`；WHILE
  `Goal/Review/Compact` 為 false THE SYSTEM SHALL 對 `/goal /review /compact` 顯示 fallback。
- **T-P3-10 (U)** WHILE backend 為 ACP THE SYSTEM SHALL 在 `/` popup 顯示 Lumen-local command 與 agent 的
  `availableCommands`，執行 agent command 時以 `"/name args"` 文字送出 `session/prompt`。
- **T-P3-11 (M)** WHEN `LUMEN_BACKEND=codex` 啟動 THE SYSTEM SHALL 與本變更前預設行為一致。
- **T-P3-12 (U)** THE SYSTEM SHALL read backend configuration only from
  `${XDG_CONFIG_HOME:-~/.config}/lumen/backends.json` and SHALL NOT read any config file from the cwd or project
  directory（在含 `backends.json` 的臨時專案目錄啟動，驗證被忽略）。
- **T-P3-13 (U)** IF `backends.json` is not owned by the current uid, or is group- or world-writable THEN THE SYSTEM
  SHALL exit with code 2 naming the file without starting any backend。
- **T-P3-14 (U)** IF `backends.json` contains an `env` key or any key outside the allowlist
  (`default`, `backends.<name>.{type,command,args}`) THEN THE SYSTEM SHALL exit with code 2 naming the key。

## P4：文件

- **T-P4-01 (M)** THE SYSTEM SHALL 在 `README.md` 與 `lumen --help` 說明 `--backend`、`LUMEN_BACKEND`、
  `/backend`、`backends.json`、Claude adapter pin 版本與預設變更，且文件中的指令範例逐一實跑可用
  （claude adapter 範例以 T2.0 spike 結果為準）。

## Out of scope（本 spec 不驗）

- Codex-via-ACP。
- `fs/*`、`terminal/*`、`elicitation/*` client capability（除非 T2.0 spike 觸發 T2.9）。
- Gemini 的完整行為（僅驗證能以 config 啟動並完成 prompt；capability 以實際 `initialize` 為準）。
