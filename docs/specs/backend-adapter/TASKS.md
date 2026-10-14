---
id: tasks-lumen-backend-adapter
title: Lumen pluggable backend - tasks
status: approved
created: 2026-10-14
updated: 2026-10-14
spec: spec-lumen-backend-adapter
---

<!-- markdownlint-disable MD025 -->
# Tasks
<!-- markdownlint-enable MD025 -->

規則：每個 task 結束時 `go test -race ./...` 必須綠；P1 全程不得改 user-visible 文案。
估時為單人專注工時。Test ID 對應 `TESTS.md`。

## Gate 0：核准

- [x] G0.1 Miyago 核准 `SPEC.md`，並回覆 Open questions #1、#2、#7（影響 P2/P3 設計）

## P1：UI 改吃 normalized events（codexBackend，零行為變更）— 2.5–3.5 天

- [ ] T1.1 定義 `backend.go`：`Event` 型別、`Capabilities`、`Backend`、`SessionInfo`、
  `CodexExtras`（0.5 天）— T-P1-01
- [ ] T1.2 `backend_codex.go`：`codexBackend` 包 `appServer`；把 `handleNotification` /
  `handleItemStarted` / `handleItemCompleted` / `handleServerRequest` 的 codex 解析搬成
  `codex method -> Event` 轉譯（含 `validateServerRequest` 下沉）（1 天）— T-P1-02, T-P1-03
- [ ] T1.3 UI event loop（`ui.go:466-500`、`screen.go:172-210`）改 select 單一 `Events()`；
  新增 `handleEvent(Event)`；**保留 shim**：`handleNotification(msg) = handleEvent(codexToEvent(msg))`、
  `handleServerRequest` / `handleItemStarted` / `handleItemCompleted` 同樣保留為 codex->Event->`handleEvent`
  的薄包裝，使既有測試直接呼叫這些函式（`lumen_test.go` 約 705、1290、1297、1314-1317、1446、1516、1827、
  1859、1894、1947、1998、2076）不需修改（0.75 天）— T-P1-04
- [ ] T1.4 approval / user-input 回應改走 `ResolveApproval` / `AnswerInput`
  （`ui.go:1470`、`1594`、`1348`、`1377`）（0.25 天）— T-P1-05
- [ ] T1.5 `native_command_handlers.go`、`dashboard.go`：通用操作走 `Backend`，codex 專屬走
  `CodexExtras` + capability 檢查，false 時顯示 fallback（0.5 天）— T-P1-06
- [ ] T1.6 `session_supervisor.go`、`lifecycle.go`、`screen.go:128` 改用 `Backend`；`factory` 型別更新
  （0.5 天）— T-P1-07
- [ ] T1.7 **前置條件：** 未修改的 `lumen_test.go` 在 T1.1–T1.6 後 `go test -race ./...` 全綠。之後才允許把測試
  改餵 normalized event 並逐步移除 shim；保留 codex 轉譯 golden test（0.5 天）— T-P1-08
- [ ] T1.8 P1 驗收：`go test -race ./...`、`go vet ./...`、手動 smoke（一個 prompt、一個 approval、
  interrupt、fork、`/goal`、reconnect）與 P1 前一致 — T-P1-09

## P2：acpBackend MVP — 3.5–5 天

- [ ] T2.0 Spike（0.5–0.75 天）：用一次性腳本實跑全域安裝的 `claude-agent-acp@0.90.0`（`npm i -g` 到隔離 prefix，不用 `npx`）與
  `gemini --acp`，只送 `initialize{protocolVersion:1, clientCapabilities:{}}` 與 `session/new`，記錄
  `agentCapabilities`、`authMethods`、省略 fs/terminal 時是否可編輯檔案、`AskUserQuestion` 的呈現、
  `session_info_update` 欄位名、adapter 預設 permission 行為（預設 mode、是否自動放行），並確認 Lumen 不傳任何
  bypass flag / env（對 `claude-agent-acp` 與 `gemini --acp` 的 argv 與環境逐項檢查）；另需記錄並寫入 `SPEC.md` Security disposition：
  (a) adapter 是否依賴 process cwd（以 `cmd.Dir` 設為非專案目錄、`session/new.cwd` 指專案目錄實測）；
  (b) 0.90.0 是否附 `npm-shrinkwrap.json`；
  (c) `settingSources` 在 0.90.0 的實際行為：user/project `.claude/settings.json` 的 allow rules 與
  `permissions.defaultMode` 是否生效；
  (d) `session/new` 回傳的 mode 與 `current_mode_update` 行為，以及 mode 不是 `default` 時 status bar 警示要用的欄位；
  (e) 確認 Lumen 不送 `session/set_mode` 或 permission 類 `set_config_option`。
  結果寫回 `SPEC.md` 把 `unverified` 改成事實。腳本放 scratchpad，不進 repo
- [ ] T2.1 `jsonrpc.go`：從 `appServer` 抽出 line-delimited JSON-RPC 2.0 conn（request/response、
  notification、inbound request、panic-safe read loop、bounded channels）；`appServer` 改用它，行為不變
  （0.75 天）— T-P2-01
- [ ] T2.2 `backend_acp.go` lifecycle：spawn（`exec.LookPath` 全域 bin；`cmd.Dir` = Lumen 自有 state dir；stderr 進有界 ring buffer，僅 `EvConnectionLost` 時顯示最後 N 行；找不到 bin 時顯示安裝指令並 disconnected）、`initialize`（`protocolVersion:1`、`clientCapabilities:{}`、
  `clientInfo`）、capability 解析、版本不符處理、stderr 擷取（不進 transcript）（0.75 天）— T-P2-02, T-P2-03,
  T-P2-13
- [ ] T2.3 session：`session/new`、`session/load` / `session/resume`、`session/list`（分頁）、replay 旗標
  （0.75 天）— T-P2-04, T-P2-05
- [ ] T2.4 `session/prompt` + `session/update` -> Event 轉譯（message / thought / tool_call /
  tool_call_update / plan / usage_update / available_commands_update / session_info_update；unknown 忽略並計數）
  （1 天）— T-P2-06, T-P2-07, T-P2-08, T-P2-14
- [ ] T2.5 `session/request_permission` <-> `EvApprovalRequest` / `ResolveApproval`（optionId 只取自 `options[]`；non-interactive / timeout / sessionId 不符 / 無法 render -> cancelled；預設 focus 不在 `allow_*`；`session/new` mode 與 `current_mode_update` 非 default 時 status bar 持續警示）；`session/cancel` 流程
  含 pending permission 回 `cancelled`、tool 標 cancelled、cancel timeout notice（0.5 天）— T-P2-09, T-P2-10
- [ ] T2.6 fake ACP agent fixture：`testdata/fakeacp/`（Go test helper，從 stdin/stdout 講 ACP，腳本化
  scenario：happy path、permission、cancel、crash、load replay、auth required、unknown update、慢回應）
  （0.75 天，與 T2.2–T2.5 交錯進行）— T-P2-11, T-P2-15
- [ ] T2.7 acp reconnect：subprocess 結束 -> `EvConnectionLost` -> supervisor 重連 -> load/resume/new
  fallback（0.25 天）— T-P2-12
- [ ] T2.8 P2 驗收：fake agent 全套 EARS 綠；真實 claude adapter 手動 smoke（prompt、approval、cancel、
  crash 後 reconnect）— T-P2-16；若 spike 顯示需要 fs 或 elicitation，開 T2.9（見 SPEC ADR-5）

## P3：Claude 預設、選擇與切換 — 1.5–2 天

- [ ] T3.1 `backend_config.go`：內建 registry（claude / gemini / codex）、只讀 `${XDG_CONFIG_HOME:-~/.config}/lumen/backends.json`、
  owner/權限檢查（uid、group/world-writable -> exit 2）、key 白名單（拒絕 `env`）、覆蓋規則、驗證（`type`、`command`）（0.5 天）— T-P3-01, T-P3-02
- [ ] T3.2 `--backend` flag、`LUMEN_BACKEND`、優先序、未知 name 錯誤；`Run` 改用 registry 建 backend；
  預設改 `claude`；非 codex backend 的 codex 專屬旗標報錯（0.5 天）— T-P3-03, T-P3-04, T-P3-05, T-P3-11
- [ ] T3.3 `/backend` slash command：picker、`/backend <name>`、busy 拒絕、atomic swap、分隔 status、
  catalog 重算（0.75 天）— T-P3-06, T-P3-07, T-P3-08
- [ ] T3.4 capability fallback 文案與 `[fork]` 條件渲染、ACP 動態 slash catalog（0.25 天）— T-P3-09, T-P3-10

## P4：文件 — 0.5 天

- [ ] T4.1 `README.md`：backend 概念、`--backend`、`LUMEN_BACKEND`、`/backend`、`backends.json` 範例、
  Claude adapter 安裝與 pin 版本說明、認證說明（不放 secret）、預設變更的 breaking note
- [ ] T4.2 `lumen --help` 文字更新；`docs/specs/lumen/SPEC.md` 加交叉參照與 Requirements 中 codex 專屬措辭
  的限定語
- [ ] T4.3 版本號 bump 建議（`appVersion`，預設改變屬 minor 以上）與 release note 草稿 — T-P4-01
