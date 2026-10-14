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
| P3 | 未開始 | |
| P4 | 未開始 | |

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
