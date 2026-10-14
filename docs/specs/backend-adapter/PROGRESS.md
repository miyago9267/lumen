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
| P2 | 未開始 | |
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
