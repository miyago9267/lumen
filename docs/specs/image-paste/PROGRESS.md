---
id: progress-lumen-image-paste
title: Lumen image paste - progress
status: done
created: 2026-10-15
updated: 2026-10-15
spec: spec-lumen-image-paste
---

<!-- markdownlint-disable MD025 -->
# Progress
<!-- markdownlint-enable MD025 -->

## 狀態

| Req | 狀態 | 測試 |
| --- | --- | --- |
| 1 clipboard reader + Ctrl+V | 完成 | T-IMG-01..06 |
| 2 chip / attachment | 完成 | T-IMG-07..10 |
| 3 backend（ACP / codex / gating） | 完成 | T-IMG-11..16 |
| 4 preview | 完成 | T-IMG-17..23 |
| 5 transcript | 完成 | T-IMG-24 |

## Probe（2026-10-15）

見 SPEC「Evidence」：ACP `promptCapabilities.image=true`；codex `localImage{path}`。

## 實作紀錄

- 新檔：`images.go`（chip / attachment / Ctrl+V 流程）、`clipboard_image.go`（osascript / wl-paste / xclip）、
  `kitty_graphics.go`（escape、box、caption、preview sync）、`image_paste_test.go`。
- `Backend.Prompt` 多 `images []promptImage`、`Capabilities.ImagePrompt`；`keyDelete`（CSI `3~`）新增。
- 偏離初稿：`ui.readClipboard` 簽名帶 `dir`；Finder 檔案改用 `POSIX path of`，不解析 file URL。
- 預覽超過 10 MiB（PNG bytes）或 webp 時只顯示 caption。

## 驗收（2026-10-15）

- `go vet ./...`、`go test -race -count=1 ./...`、`go build ./cmd/lumen` 通過。
- tmux 手動 smoke（macOS，真實 `osascript` PNGf -> `/tmp/lumen-dev`，預設 claude backend，未送 prompt）：Ctrl+V 插入
  `[Image #1]`，caption `Image #1 · 64x32 · PNG · 2 KB · <tmp path>` 出現；第二次 Ctrl+V 得 `#2`；Backspace 整段刪除 chip。
  剪貼簿原內容（純文字）已還原。
- 真實 `claude-agent-acp@0.90.0` initialize：`promptCapabilities.image=true`（未開 session / 未送 prompt）。

## 未驗證

- 真實 Ghostty / kitty / WezTerm 的 graphics 顯示（tmux 不轉送 kitty graphics，只驗 caption）。
