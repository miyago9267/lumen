---
id: progress-lumen-backend-adapter
title: Lumen pluggable backend - progress
status: in-progress
created: 2026-10-14
updated: 2026-10-14
spec: spec-lumen-backend-adapter
---

<!-- markdownlint-disable MD025 -->
# Progress
<!-- markdownlint-enable MD025 -->

## 狀態

| Phase | 狀態 | 備註 |
| --- | --- | --- |
| G0 | 完成 | Miyago 核准 SPEC（2026-10-14），ADR-4 自寫 ACP client、JSON config、預設改 Claude 均已接受 |
| P1 | T1.1-T1.7 完成；T1.8 待手動 smoke | `go test -race ./...`、`go vet ./...`、`gofmt -l .` 皆乾淨 |
| P2 | T2.0-T2.7 完成；T2.8 待真實 adapter 手動 smoke | fake agent 全套 T-P2 綠（T-P2-16 為 M，未跑） |
| P3 | T3.1-T3.4 完成；T-P3-11 為 M（僅以假 codex 驗證啟動路徑） | `go test -race -count=1 ./...`、`go vet ./...`、`gofmt -l .` 皆乾淨 |
| P4 | T4.1-T4.3 完成 | README、`--help`、lumen SPEC 交叉參照、`appVersion` 0.4.0；`go test -race -count=1 ./...`、`go vet ./...`、`gofmt -l .` 皆乾淨 |

## P1 驗收（2026-10-14）

- T-P1-01、02、03、04、05、06、07、08、10：自動測試通過。
- T-P1-09（手動 smoke）：尚未用真實 codex 跑。已用假 `codex app-server`（`LUMEN_CODEX_BIN`）對照 P1 前後的 binary：
  inline 模式一個完整 turn 的 stdout 逐 byte 相同；fullscreen（pty）的 prompt、approval（按 `a` 後 codex 收到
  `{"decision":"accept"}`）、連線中斷後 reconnect 行為一致。真實 codex 的 interrupt、fork、`/goal`、`/compact`
  仍待 Miyago 手動確認。

## P1 實作與 spec 的差異

1. `SessionInfo` 是 `threadSummary` 的 type alias，不是另立型別。原因：既有測試與 dashboard 直接構造
   `threadSummary{Turns: []historyTurn...}`；history normalized 化留到 P2 一併處理。
2. `EvApprovalRequest` 把 `Detail` 拆成 `CWD` 與 `Reason`，因為 UI 目前分行顯示兩者；`CWD` 為空時 UI 用 session cwd。
3. 新增 `EvRequestRejected{ReqID, Message}`：backend 已替 codex 回覆 error，UI 只負責顯示 `request rejected · ...`
   （原文案不變）。
4. `Backend` 新增 `RejectRequest`（非互動終端拒絕 request，維持原 `-32001` 與文案）與 `Disconnected()`
   （fullscreen 首次 prompt 失敗時判斷連線是否已死）。`Open(ctx)` 的 ctx 目前 codex 不使用。
5. `EvToolStarted.Title` 是 UI 逐字顯示的文字（`$ cmd`、`✎ file change`、`◇ server/tool`）；`Command` 只供 recap。
6. `EvUsage` 暫無 `Cost`（P2 的 ACP `usage_update` 再加）。
7. 校準既有行為的兩個差異（皆為 fail-closed 或更保守的方向，非 UI 文案）：
   - 非互動 inline 模式下，`item/permissions/requestApproval` 與格式錯誤的 request 由 backend 直接回覆
     （空 grant / error）後繼續，不再以「approval requires an interactive terminal」結束；approval 與
     user-input request 仍然結束。
   - 連線結束時 pump 會先送完已排隊的 notification 再送 `EvConnectionLost`（原本 select 隨機，可能吃掉最後一段輸出）。
8. inline 模式下 fileChange 完成且 status 為空字串時，不再印出空白狀態行（codex 實際一定帶 status）。
9. 驗證順序：T1.1-T1.6 完成後，未修改的 `lumen_test.go` 在 `go test -race ./...` 全綠
   （當時 `ui.client`/factory 仍是 `*appServer`，以 `be()` 包裝）。T1.6 的 factory/`reconnectResult` 型別更新因既有測試
   釘住 `func() (*appServer, error)` 與 `ui{client: ...}`，延到 T1.7 隨測試 constructor 一起改；測試改動僅限
   constructor（`client:` -> `backend: newCodexBackend(...)`、factory 回傳 `Backend`、`showApproval` 的 id 參數），
   無新增 skip，斷言未變。
10. 原本 UI 內的 `handleNotification` / `handleItemStarted` / `handleItemCompleted` / `handleServerRequest` shim
    移到 `backend_codex_shim_test.go`（codex -> Event -> `handleEvent`），production 不再保留。

## P2 實作（2026-10-14）

驗證：`go test -race -count=1 ./...`、`go vet ./...`、`gofmt -l .` 皆乾淨；ACP 相關測試 `-count=6` 穩定。

偏差與決定：

1. fake agent 放在 `internal/lumen/fakeacp_test.go`（test binary 以 helper process 重新執行自己），不是
   `testdata/fakeacp/` 的獨立 program；不需要在測試中另外 `go build`。
2. 抽出的共用 conn 是 `rpcConn`（`jsonrpc.go`），`appServer` 內嵌它；read loop 改成逐行讀取（行長上限 32MB），
   malformed 行只計數略過（codex 路徑也一樣；原本 decode error 會結束連線）。ACP 的 request / notification 由
   read loop 依線路順序同步處理（`handle`），因此 `session/load` 的重播 update 一定在 response 前處理完。
3. `session/load` 的重播不產生 live event，而是收成 `SessionInfo.Turns`（沿用 `historyTurn`），由 UI 既有的
   replay 去重路徑渲染；reconnect 時不會重複寫入 transcript。
4. approval 回覆為 ACP schema 的巢狀形式 `{outcome:{outcome:"selected",optionId}}`。使用者取消用保留值
   `CancelApprovalID`（避免與 agent 自己的 `cancel` option id 衝突，例如 Gemini）。
5. 新增 `EvApprovalRequest.Explicit`：UI 進入 explicit 模式（數字鍵選 option、Enter 不做決定、Esc/Ctrl-C 取消）。
   codex 的 approval 路徑不變。新增 `EvModeWarning`（status bar 持續顯示）、`EvCommandsUpdated`、`EvUsage.Cost`。
6. 找不到 session 時 ACP 回傳 `errSessionNotFound`，`canStartFreshThreadAfterResume` 同時接受它與 codex 的
   `no rollout found`；transcript 為空才開新 session（與 T-P1-07 一致）。T-P2-12 規格中的「附 notice」尚未做
   （reconnect 目前只顯示 `reconnected · <id>`）。
7. `initialize` 的 `authMethods` 非空一律視為需要登入（依 T-P2-13）。Gemini 的 `authenticate` 流程屬 P3 以後。
8. `claudeACPConfig()` 為內部 constructor（`SessionMeta` 帶 `allowDangerouslySkipPermissions:false`）；未接任何
   `--backend` / `/backend` / `backends.json`（P3）。
9. 兩個 P1 follow-up（verifier）：`codexBackend.Prompt` 回傳後立即記錄 turn id（含先紅後綠測試）；T-P1-07 文字修正。

未完成：T2.8 真實 `claude-agent-acp` 手動 smoke（prompt、approval、cancel、kill 後 reconnect）與 T-P2-16
結果記錄；依指示本輪未跑真實 model prompt。

## P3 實作（2026-10-14）

驗證：`go test -race -count=1 ./...`、`go vet ./...`、`gofmt -l .` 皆乾淨。T-P3-01..10、12..14 自動測試通過；
T-P3-11（M）以假 `codex`（`LUMEN_CODEX_BIN`）確認 `LUMEN_BACKEND=codex` 啟動的仍是
`codex app-server --listen stdio:// ...` 且 codex 旗標被接受，未用真實 codex 跑 prompt。

偏差與決定：

1. `backends.json` 同名 entry 整筆取代內建（不逐欄位合併）。`type: codex` 的 entry 不接受 `command` / `args`
   （明確報錯，不默默忽略）。Claude 的 `allowDangerouslySkipPermissions:false` 以 command / args 是否含
   `claude-agent-acp` 判斷是否附上（涵蓋使用者明確 opt-in 的 `npx`）；安裝指令只在直接用 `claude-agent-acp` 時顯示。
2. 相對的 `XDG_CONFIG_HOME` 視為無效並退回 `~/.config`（避免設定檔位置被導向 cwd）。owner / mode 以 `os.Stat`
   （跟隨 symlink）檢查目標檔；不檢查上層目錄權限。
3. 設定錯誤、未知 backend、codex 專屬旗標在非 codex backend 一律 exit 2，且發生在檢查 TTY 與啟動任何 backend 之前。
   找不到 `claude-agent-acp` 時啟動以 exit 1 結束並印出安裝指令（不自動下載、不用 npx）。
4. `--resume` / `--continue` 對沒有 `SessionResume` / `SessionList` 的 backend 明確報錯（`backend X cannot ...`）。
5. `/backend`：無參數在 fullscreen 開 modal picker（上下 / Enter / 數字鍵 / Esc），inline 模式列出清單並提示
   `/backend <name>`。picker 對非目前 backend 只顯示種類與 command basename，目前者附即時 capability。
   重連中（`u.backend == nil`）也拒絕切換，避免與 reconnect supervisor 競爭；切到同名 backend 為 no-op。
   切換成功後 `overrides`、`agentCommands`、mode 警示全部重置；supervisor 的 factory 每次重連才讀
   `u.serverFactory`，所以重連的是目前 backend。
6. Slash catalog 依 backend 即時計算（`SlashCatalog == "dynamic"`）：Lumen-local（help、status、dashboard、backend、
   copy、diff、pwd、usage、statusline、clear、new、resume、stop、quit）加上 agent 的 `availableCommands`
   （與 local 同名者以 local 為準）；其餘 codex catalog 指令顯示 `/<cmd> is not supported by backend <name>`，
   不送出 prompt，也不記錄 codex runtime override。agent command 以 `"/name args"` 文字送 `session/prompt`，
   busy 時進 queue。
7. `[fork]` 與其 tooltip 只在 `ForkAtTurn` 為 true 時渲染；沒有 backend（重連中）時沿用 codex 行為。
   Dashboard / Ctrl+U：沒有 `SessionList` 顯示 `listing sessions is not supported by backend X`，沒有
   `SessionResume` 時 attach 與 `/resume` 顯示 fallback；dashboard 說明文字用目前 backend 名稱。
8. T-P2-12 leftover：reconnect 退回新 session 時（`reconnectResult.freshSession`）顯示
   `agent context lost · started a new session`。
9. 驗證者 follow-up（P2）：ACP permission 逾時新增 `EvApprovalExpired`（UI 清除對應 card 並顯示狀態列）；
   explicit approval 在 card 出現後 400ms 內忽略數字鍵（取消鍵不受限，clock 可注入）。
10. 流程備註：T3.1 的實作先於測試寫成（測試一次通過，沒有先紅）；T3.2 之後皆先寫失敗測試。

未完成：T1.8、T2.8 的真實 codex / claude adapter 手動 smoke。

## P4 實作（2026-10-14）

- T4.1：`README.md` 改寫 intro / 安裝，新增 Backend 章節（選擇順序、`/backend`、`backends.json`、
  permission 行為、capability fallback、breaking-change note）。
- T4.2：`lumen --help` 加 backend 說明與 adapter 安裝指令，codex 專屬旗標標註 `(codex backend only)`；
  `docs/specs/lumen/SPEC.md` 加交叉參照並在 codex 專屬 Requirements 加「codex backend 時」限定語。
- T4.3：`appVersion` 0.3.1 -> 0.4.0（預設 backend 改變，minor bump）。

### Release notes 草稿（0.4.0）

- Breaking：預設 backend 由 `codex` 改為 `claude`（ACP，`claude-agent-acp`）。維持舊行為：
  `LUMEN_BACKEND=codex` 或 `backends.json` 設 `"default": "codex"`。
- 新增 `--backend`、`LUMEN_BACKEND`、`/backend`、`~/.config/lumen/backends.json`（owner / mode 檢查，不支援 `env`）。
- 新增 `gemini --acp` backend；codex backend 保留 fork / goal / review / compact。
- Claude adapter 需自行安裝：`npm i -g @agentclientprotocol/claude-agent-acp@0.90.0`，Lumen 不會自動下載。
- Permission 永不自動允許；非 default mode 顯示警示；approval card 出現後 400ms 內忽略數字鍵。
- codex 專屬旗標（`--model` 等）搭配其他 backend 會 exit 2。

未完成：T1.8、T2.8 的真實 codex / claude adapter 手動 smoke。

## 真實 adapter smoke 後修正（2026-10-14）

驗證：`go test -race -count=1 ./...`、`go vet ./...`、`gofmt -l .` 皆乾淨；每項修正先寫失敗測試。

1. reconnect 重複 transcript（T-P2-12）：根因是 `adoptReconnected` 無條件把 `thread.Turns` 重播進 transcript，
   ACP `session/load` 的 replay turn 沒有 turn id，`screenReplayAddForTurn` 的 turn id 合併對不上，所以整段 history
   被當成新 turn 寫入（codex 有 turn id，仍走原本的依 id 合併）。修正：transcript 已有內容時略過沒有 turn id 的
   replay turn 並保留原 `Turns`；ACP 新增 `ReconnectSession`（supervisor 在已有 transcript 時優先呼叫），
   有 `session/resume` 就用 resume（不重播），只有 `session/load` 時照常 load 但丟棄 replay。沒有 transcript 的
   重連（例如 `--resume` 後立即斷線）仍用 load 取回 history。fakeacp 原本就是先送 update 再回 result，與真實 adapter 一致。
2. `[fork]`：重連中（backend nil）改用 `markConnectionLost` 記下的最後已知 capability；從未知到過則隱藏
   （修正偏差 7 的「沿用 codex 行為」）。
3. `session_start` 事件列顯示目前 backend 名稱（codex 維持 `[app-server]`）。
4. turn 失敗原因：codex `error` notification 實為 `{error:{message}, willRetry}`（由 `codex app-server
   generate-json-schema` 確認），舊解析讀頂層 `message` 得到空字串，原因因此遺失；`turn/completed` 的 `turn.error`
   也未讀取。main 同樣有此問題，屬既有缺陷的改善。現在兩者都解析：`willRetry` 以 warning 顯示，turn 失敗的
   `EvTurnCompleted.Err`（codex 與 ACP）由 UI 顯示為 error 列，同一則訊息已由 notice 顯示時不重複。

## Manual smoke 2026-10-15

環境：真實 `claude-agent-acp@0.90.0`，lumen build 自 `8312796`。
證據：`/private/tmp/claude-501/-Users-miyago-Project-Active-Tools-lumen/10ab9ec2-53ef-4373-9ec1-b43d3de55b64/scratchpad/smoke/evidence/`（session-local，非 durable，不入版控）。

- T-P2-16 PASS（claude adapter）：
  - approval card 顯示 `$ git push --force origin main` 與 `[1] Yes [2] No`；card 出現後 55ms 內的數字鍵被忽略（400ms guard）。
  - card pending 時 Ctrl-C：status 為 cancelled、指令未執行、turn completed；超過 400ms 後 allow-once：指令確實執行。
  - turn 進行中 Ctrl-C：turn 被 interrupt。
  - `kill -9` adapter：reconnecting -> reconnected，只有 1 個 turn（無 replay 重複），重連期間沒有 `[fork]`，label 維持 `[claude]`。
  - user defaultMode 為 auto 時顯示 `permissions: auto (from agent settings)`，default mode 時不顯示。
  - 備註：user settings 有 allow `Bash(*)`/`Write(*)`/`Edit(*)` 時不會出現 card，只有 `ask` 規則會觸發；這是 adapter 預期行為，Lumen 只在非 default mode 顯示 mode warning。
- T-P3-06 PASS：`/backend` picker 列出 claude / gemini / codex。
- T-P3-11 PASS（僅 launch/argv）：`codex app-server --listen stdio:// --config suppress_unstable_features_warning=true`，無 bypass flag；`[fork]`、`/goal`、compact 指令有提供。
- codex 失敗原因已能顯示（真實 400「gpt-6-sol not supported with ChatGPT account」）。
- T-P1-09 BLOCKED：真實 codex 的 turn/interrupt、fork、`/goal`、compact 執行無法驗證，因 Miyago 的 codex config model `gpt-6-sol` 被 ChatGPT-account auth 拒絕（`codex exec` 同樣錯誤）。需 Miyago 修正 codex model 設定後重跑。
- Gemini runtime 未驗證（無 auth；IneligibleTierError）。

## /model 支援（ACP）2026-10-15

Probe：真實 `claude-agent-acp@0.90.0`，只送 `initialize` + `session/new` + `session/set_config_option`（沒有 `session/prompt`，不花 model 額度）。

- `session/new` result keys：`sessionId, modes, configOptions`；沒有 `models`（unstable `models.availableModels` / `session/set_model` 在此版本不存在，Lumen 僅保留為 fallback）。
- `configOptions` 內 `{id:"model", category:"model", type:"select", currentValue:"opus", options:[{value,name,description}]}`，**flat**（非 group）。13 個選項：`default, opus, fable, sonnet, haiku, claude-haiku-4-5-20251001, claude-sonnet-5, claude-opus-5, claude-fable-5, claude-opus-4-8, claude-opus-4-7, claude-opus-4-6, claude-sonnet-4-6`；`value` 是 alias（`opus`）或完整 model id，`name` 是人類可讀（`Opus 5.5`）。清單依帳號而定。
- 其他 config option：`mode`(category mode)、`effort`(thought_level)、`fast`(model_config)、`agent`（無 category）。
- `session/set_config_option{sessionId,configId:"model",value}` 回 `{configOptions:[...全部...]}`，`currentValue` 已更新；未帶 `models`。
- Lumen 實作：ModelList/ModelSwitch caps 在 session/new|load|resume 的 result 或 `config_option_update` 含 category=model 的 option 時為 true；grouped options 會展平；`SetModel` 先在本地驗證 id（精確，其次不分大小寫比對 value / name），未知時回 `unknown model "x"; available: ...` 且不送 request。`config_option_update` 改變 current 時送 `EvModelChanged`，UI 更新顯示的 model。
- `/model`（無參數 / `list`）與 `/model <id>` 走 backend-neutral 的 `ModelPicker` / `ModelSetter`；codex 實作 `ModelPicker`（`model/list`），沒有 `ModelSetter`，`/model <id>` 維持 per-turn override。`/model ` 的參數補全（Tab 補 id、Enter 執行）在 codex 與 ACP 共用；ACP 的 dynamic catalog 在 `ModelList` 為 true 時多出 `/model`。
- 發現（未處理）：以真實 adapter 跑 `go test`，測試結束後 adapter 的子孫 process 仍持有 stdout pipe，使 `go test ... | tail` 不返回（`Close` 後）。測試本身 PASS；是否為 Close 沒清掉 process group 待查。
- 未驗證：live TUI 手動操作 `/model` + Tab（只用 unit/fake-adapter 測試與真實 adapter 的 backend 層 probe 覆蓋）。
