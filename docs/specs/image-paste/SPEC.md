---
id: spec-lumen-image-paste
title: Lumen image paste (Ctrl+V chip, attachments, preview)
status: implemented
created: 2026-10-15
updated: 2026-10-15
author: Miyago
approved_by: Miyago 2026-10-15
tags: [lumen, tui, acp, codex, clipboard, image, kitty-graphics]
priority: medium
depends_on: [spec-lumen, spec-lumen-backend-adapter, spec-lumen-transcript-ux]
---

<!-- markdownlint-disable MD025 -->
# Lumen image paste
<!-- markdownlint-enable MD025 -->

> 狀態：draft -> implemented（見 PROGRESS.md）。範圍：Ctrl+V 讀系統剪貼簿圖片、composer chip、
> 送進 ACP / codex、screen mode 的預覽。體驗參考 grok-cli。

## Goal

在 composer 按 Ctrl+V，若剪貼簿是圖片，就在游標處插入 chip `[Image #N]`，圖片隨該次 prompt 送給 backend。
游標停在 chip 上（或剛好在 chip 後）時，screen mode 在 composer 上方顯示預覽：支援 kitty graphics 的
terminal 畫出實際圖片，其餘只顯示一行 caption。

## Non-goals

- 不做 drag-drop / 貼上檔案路徑自動轉 chip。
- inline mode（非 screen mode）不做預覽（chip 與送出仍可用）。
- 歷史 transcript 不渲染圖片；user block 只顯示含 chip 的原文。
- 不做 Windows clipboard；不支援 webp 預覽（可送出，caption only，因 stdlib 無 webp decoder 且 kitty `f=100` 只收 PNG）。
- 不改 Cmd+V / bracketed paste 的文字行為。

## Decisions

### D1 剪貼簿讀取（可注入）

`ui.readClipboard func(dir string) (clipboardContent, error)`（`dir` 為圖片 temp 目錄），預設 `readSystemClipboard`；測試注入。
`clipboardContent{ImagePath string; Temp bool; Text string}`。

- macOS：先用 `osascript -e 'clipboard info'` 看型別；含 `«class furl»`（Finder 複製的檔案）時以
  `POSIX path of (the clipboard as «class furl»)` 取路徑，副檔名為 png/jpg/jpeg/gif/webp 且內容確實是圖片就直接用該檔
  （`Temp=false`，不刪），否則當作沒有圖片；含 `PNGf`（或 `TIFF`）時用 `osascript` 把 `«class PNGf»` 寫到
  `<tmp>/lumen-images-*/clip-*.png`（無 cgo）。
- Linux：依序 `wl-paste -t image/png`、`xclip -selection clipboard -t image/png -o`，stdout 寫到 temp 檔。
- 圖片一律以內容 sniff（`http.DetectContentType`）驗證為 png/jpeg/gif/webp，不信任副檔名。
- 沒有圖片時 `ImagePath==""`，再以 `pbpaste` / `wl-paste` / `xclip -o` / `xsel` 讀文字放進 `Text`。

### D2 無圖片時的行為

fallback 成「把剪貼簿文字當 paste 插入」（與 Cmd+V 同路徑 `insertText`，需正規化）；文字也是空就顯示 muted
`clipboard has no image`。理由：Ctrl+V 在其他 app 就是貼上，貼不到圖片時貼文字最不意外；不會有「按了沒反應」。
讀取失敗（命令不存在等）顯示 muted `clipboard unavailable: <err>`，不改變 input。

### D3 Chip 與 attachment

- N 為 session 內遞增計數（`ui.imageSeq`），不重用。`ui.images map[int]*imageAttachment`
  （`Path, MIME, Size, Width, Height, Temp, PNG []byte`），尺寸由 `image.DecodeConfig`；無法解碼（如 webp）
  時 `Width/Height=0`，caption 省略尺寸。
- chip 文字 `[Image #N]`；僅「attachment 存在」的 chip 視為 chip，手打的 `[Image #9]` 只是文字。
- Backspace 在 chip 內或剛好在 chip 後 -> 整個 chip 刪除；Delete（新增 `keyDelete`，CSI `3~`）在 chip 內或 chip 起點
  -> 整個 chip 刪除，其餘情況刪一個 rune。
- 送出：`imagesInText(text)` 依文字中仍存在的 chip（依出現順序、去重）取 attachment；chip 文字保留在 prompt。
  因為由文字重新推導，queue / cancel-and-send / `inputHistory` 召回都自然帶著附件，不需另外存。

### D4 Backend interface

`Backend.Prompt(sessionID, text string, images []promptImage, rt runtimeOverrides)`；
`promptImage{Path, MIME string}`。`Capabilities.ImagePrompt bool`。

- ACP：`initialize` 解析 `agentCapabilities.promptCapabilities.image` -> `Caps().ImagePrompt`。
  `session/prompt.prompt` = text block + 每張 `{"type":"image","mimeType":...,"data":<base64>}`。
  在 `Prompt()` 同步讀檔與編碼（失敗直接回 error，不開 turn）。`ImagePrompt==false` 而 images 非空 -> error。
- Codex：`ImagePrompt=true`；`turn/start.input` 追加 `{"type":"localImage","path":...}`（欄位名見下方 evidence）。
- UI：Ctrl+V 前檢查 `Caps().ImagePrompt`；不支援 -> muted `<backend> does not accept images`，不插入。
  送出時文字含 chip 但 backend 不支援（例如切換 backend 後）-> 回 error、prompt 不送、input 保留。
  queue / cancel-and-send 在入列時做同樣檢查。

### D5 預覽（screen mode 限定）

- 可見規則：`screenMode` 且無 approval/inputRequest/dashboard/picker，且游標在 chip 內（`start < cursor <= end`，
  即 chip 上或剛好在後面）或恰在 chip 起點。多個 chip 相鄰時取 `start < cursor <= end` 的那個。
- 版面不變：預覽覆蓋 transcript body 底部數列（這幾列在 frame 中輸出空白），caption 放在原本空白的
  composer gap row（`screenComposerGapRows`）。沒有 focus chip 時 frame 與現況逐字相同。
- Caption：`Image #N · WxH · PNG · 230 KB · <path>`（尺寸未知則省略該欄；超寬以 `truncateDisplay` 截斷，
  截 path 尾端前保留前綴）。所有 terminal 都顯示。
- Kitty graphics 偵測：`TERM_PROGRAM` 為 `ghostty` / `kitty` / `WezTerm`，或 `TERM` 含 `kitty` / `ghostty`。
  `TERM_PROGRAM=tmux` 不算。
- 傳輸：direct、chunked base64（每 chunk 4096），`a=t,f=100,i=<N>,q=2`，每張圖每個 process 只送一次
  （`ui.kittySent`）；不使用 `t=f`，以免 ssh / sandbox 下 terminal 讀不到 path。非 PNG（jpeg/gif）先用 stdlib
  decode 後重新編成 PNG；webp 不傳。
- 顯示：每次 `renderScreen` 在 frame 之後以 cursor 定位送 `a=p,i=<N>,p=1,c=<cols>,r=<rows>,C=1,q=2`
  （同 image id + placement id 的 place 會取代舊 placement，不重傳資料，也使 terminal 覆寫文字後仍能恢復）。
  最大 40x12 cell，依 PNG 長寬比與假設 cell 寬:高 = 1:2 縮放，並受 body 可用列數限制。
- 刪除：預覽隱藏時送 `a=d,d=i,i=<N>,p=1,q=2`（只刪 placement）；結束（`runFullscreen` return）送
  `a=d,d=I,i=<N>,q=2` 對所有已傳送 id 釋放資料。resize 時 placement 以同 ID 取代，不另處理。

### D6 Temp 檔

clipboard 抽出的圖片放 `os.MkdirTemp("", "lumen-images-")`（mode 0700），`Temp=true` 的檔在 `run*` 結束時
整個目錄移除。Finder file 參照原檔，不複製、不刪。

## Evidence

- 真實 `claude-agent-acp@0.90.0` `initialize`（無 session/prompt，probe process 已 kill）：
  `agentCapabilities.promptCapabilities = {"image":true,"embeddedContext":true}`。
- Codex app-server schema（`codex-cli 0.162.1`，`codex app-server generate-json-schema --out <dir>`，
  `v2/TurnStartParams.json#/definitions/UserInput`）：`LocalImageUserInput = {type:"localImage", path:string,
  detail?:ImageDetail|null}`（required：`path`, `type`）；另有 `{type:"image", url|fileId}`。使用 `localImage`。

## 對其他 spec 的影響

`docs/specs/backend-adapter/SPEC.md` 的 `Backend` 簽名：`Prompt` 多一個 `images` 參數；`Capabilities` 多 `ImagePrompt`。

## Test strategy

見 `TESTS.md`。unit test + fake ACP agent（`FAKEACP_CAPS` 加 `image`）+ pipedCodex；不送真 model prompt。
kitty graphics 以 escape 字串斷言；真 Ghostty 顯示為手動項目（PROGRESS.md「未驗證」）。
