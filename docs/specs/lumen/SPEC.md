---
id: spec-lumen
title: Lumen terminal UI
status: in-progress
created: 2026-09-15
updated: 2026-09-15
author: Miyago
approved_by:
tags: [codex, tui, app-server, ux]
priority: high
---

<!-- markdownlint-disable MD025 -->
# Lumen terminal UI
<!-- markdownlint-enable MD025 -->

## Goal

保留 Codex 的 model、app-server、sandbox、skills、MCP、approval 與 session
harness，重做 terminal interaction，提供接近 Grok Build default shell 的
full-screen app shell 操作體驗，並保留 scrollback fallback。

## Requirements

- 官方 `codex` binary、設定、session store 與既有 launcher 維持可用。
- 新入口必須能啟動本機 `codex app-server`，不把 credentials 放進 command
  arguments、logs 或 repo。
- 第一個 vertical slice 必須支援：new thread、resume thread、prompt
  input、assistant streaming、tool progress、approval request、tool user input
  與 interrupt。
- 預設採 full-screen app shell rendering：top status bar、中央 transcript、固定
  composer 與 footer shortcuts 必須同時可見。
- `--minimal` 與 `--no-alt-screen` 提供 inline/scrollback rendering，保留 terminal
  scrollback 與低干擾 fallback。
- UI 必須讓 model、reasoning effort、sandbox、approval policy、cwd 與 thread
  name 可見，不偷偷覆蓋 Codex config。
- app-server protocol version 必須與執行中的 `codex` 對齊；protocol failure
  要明確顯示並安全退出。
- `lumen` 與官方 `codex` 分開安裝、分開 rollback，更新官方 CLI 不得覆蓋
  custom frontend。

## Non-goals

- 第一階段不 fork 或 patch 官方 Codex binary。
- 第一階段不重做 desktop app、cloud workflow 或 Codex model backend。
- 第一階段不自行實作 sandbox、approval policy、MCP 或 tool execution。
- 不為了 UI 方便而把現有 approval policy 改成自動允許。

## Architecture / Plan

### Decisions

- **App-server seam:** custom frontend 透過 `codex app-server` 的 stdio JSONL
  protocol 連線。
  - **Reason:** 官方 app-server 已承載 thread lifecycle、streamed events、
    approvals、history 與 tool loop；frontend 可以獨立演進。
  - **By:** Miyago (2026-09-15)
- **Separate entrypoint:** 第一個命令命名為 `lumen`，保留 `codex` 作為
  fallback。
  - **Reason:** 可立即試用並保留清楚 rollback 邊界。
  - **By:** Miyago (2026-09-15)
- **Native binary:** frontend 先用 Go 建置，依賴維持在 `tools/lumen/`。
  - **Reason:** macOS 啟動快、可產生單一 binary，且 repo 已有 Go tool convention。
  - **By:** Miyago (2026-09-15)
- **Full-screen default:** interactive TTY 預設進入 redraw-based app shell，inline
  mode 由 `--minimal` / `--no-alt-screen` 明確選擇。
  - **Reason:** Grok CLI 的主要操作感來自固定 composer、transcript viewport 與
    status/footer 分層；inline 仍保留作 scrollback 與故障 fallback。
  - **By:** Miyago (2026-09-15)
- **Standalone repository:** frontend source、installer 與 spec 從 dotfile 移到
  `/Users/miyago/Project/Active/Tools/lumen`；dotfile 只保留入口說明。
  - **Reason:** UI 要能獨立演進、測試與 rollback，不和 global dotfile release 綁定。
  - **By:** Miyago (2026-09-15)
- **Lumen dark dock:** 保留暗色 status/composer 皮，但改用 conversation cards、event
  rail、compact footer 與 tooltip，避免複製 Grok 的畫面結構。
  - **Reason:** 借鑑互動層級，不複製品牌或 layout。
  - **By:** Miyago (2026-09-15)
- **Terminal-native editor:** prompt 以 rune buffer 管理；方向鍵支援游標與 prompt
  history，SGR mouse click 只負責 composer cursor placement。
  - **Reason:** 不引入大型 TUI dependency，維持低啟動成本與可測試 seam。
  - **By:** Miyago (2026-09-15)
- **Deterministic rich text:** Markdown fenced code、heading/list/quote 與常見
  Mermaid flowchart 由 frontend 做安全的 terminal preview，不執行 Markdown 或
  Mermaid 內容。
  - **Reason:** 渲染是 presentation-only，不應增加外部 process 或 network surface。
  - **By:** Miyago (2026-09-15)
- **Session supervisor:** `Ctrl+\\` / `/dashboard` 在同一個 pager 內呼叫
  `thread/list`，以 preview row 管理 top-level sessions；`1–9` / `Enter` attach，`n`
  建立新 session，`r` refresh。
  - **Reason:** Grok 的核心差異是 pager 作為 process/session supervisor；先落一條真實
    app-server vertical slice，再擴充 background task、permission routing 與 pin/rename。
  - **By:** Miyago (2026-09-15)
- **Interruption semantics:** idle turn 的 `Enter` 送出；busy turn 的 `Enter` 排隊，
  `Ctrl+Enter` interrupt current turn 後接手；`/btw` 保留為 future independent aside
  turn，不把它偽裝成同一個 turn。
  - **Reason:** queue、cancel-and-send、aside 是三種不同意圖，不能共用一個插話行為。
  - **By:** Miyago (2026-09-15)

## Tasks

- [x] Phase 1: app-server client 與 protocol probe
- [x] Phase 2: inline transcript、prompt composer、streaming event renderer
- [x] Phase 3: approval、interrupt、resume 與 model/status controls 的第一版
- [x] Phase 4a: installer、rollback 與 user-facing docs 的第一版
- [ ] Phase 4b: session picker 與 richer launcher controls
- [x] Phase 5a: full-screen app shell、固定 composer、shortcuts overlay、transcript
  viewport 與 layout tests
- [x] Phase 5a.1: app-server request routing、user-input composer 與 fail-closed
  permission response
- [x] Phase 5a.2: thread token usage notification 與 top-bar context meter
- [x] Phase 5a.3: transcript PageUp/PageDown viewport navigation
- [x] Phase 5a.4: per-process suppression of the unstable-feature startup banner
- [x] Phase 5a.5: mouse-wheel scrolling、tool output collapse 與 turn recap
- [x] Phase 5b.1: prompt arrow/history editing、SGR mouse cursor placement
- [x] Phase 5b.2: conversation cards、hover tooltip 與 Lumen dark dock visual variant
- [x] Phase 5b.3: Markdown blocks、code blocks 與 Mermaid flowchart preview
- [x] Phase 5b.4: source、installer、spec 抽成 standalone repository
- [ ] Phase 5c: fresh-context verification、per-tool expansion、session supervisor
  parity
  與 richer session controls

目前 Phase 3 已有 approval、interrupt、resume、`/help`、`/status` 與明確的
model、reasoning、sandbox、approval overrides；model picker、session picker 與
richer controls 尚未納入。Phase 5a 已將 interactive default 改為 full-screen，
`--minimal` 保留 inline fallback。

## Evidence

- [x] `go test -race ./...` passes。
- [x] binary build、`--help` 與 `--version` passes。
- [x] isolated installer test builds into a temporary `HOME` without touching the
  real `~/.local/bin`。
- [x] local one-shot smoke used `codex app-server` and returned streamed `READY`
  after `turn/completed`。
- [x] PTY smoke verified raw input、`/help`、`/status`、`Alt+Enter` 與 `Ctrl-C`；
  `--continue` 也已 render persisted recent history。
- [x] fixed-size screen model tests verified top bar、transcript、CJK wrapping、固定
  composer、footer、shortcuts overlay 與 approval card。
- [x] app-server fixture tests verified approval routing、逐題 user-input response、
  secret-safe input rendering、permission empty grant 與 thread mismatch rejection。
- [x] `thread/tokenUsage/updated` fixture verified top bar context meter formatting。
- [x] PageUp/PageDown escape-sequence parsing 與 bounded transcript viewport verified。
- [x] app-server launch args include per-process `suppress_unstable_features_warning=true`；不
  修改 `CODEX_HOME/config.toml`。
- [x] PTY input model 與 screen tests verified mouse-wheel scroll events、collapsed
  tool output、`Ctrl+O` toggle 與 compact turn recap。
- [x] unit tests verified prompt history、mouse cursor placement、conversation card
  layout、hover tooltip、Markdown/Mermaid preview、Dashboard preview 與 queue/
  cancel-and-send parsing。
- [x] PTY tests verified prompt history、mouse cursor placement、Dashboard attach
  與
  Ctrl+Enter terminal encoding。
- [x] standalone repo builds and installs from its own `install.sh`; dotfile source
  copy is removed only after standalone verification。
- approval-producing write turn、fresh-context verification 與後續 UX tuning remain
  open。

## First acceptance slice

- [x] `go test ./...` 通過。
- [x] `lumen --help` 顯示入口與 fallback 說明。
- [x] local protocol smoke 能完成 `initialize`、`initialized`、`thread/start`。
- [x] interactive run 能送出一個 prompt，顯示 streamed assistant output，並在
  `turn/completed` 後回到可輸入狀態。
- [x] interactive default 顯示 full-screen app shell；`--minimal` 可切回 inline
  scrollback mode。
- [x] custom frontend 不會修改官方 Codex binary 或既有 config/session 檔案。

## Files

- `main.go`, `ui.go`, `screen.go`, `main_test.go` - custom frontend source and tests
- `install.sh`, `uninstall.sh` - standalone local binary installer and rollback
- `README.md` - setup, interaction and rollback notes

## Notes

Grok Build 目前的 default shell 有固定 composer、transcript viewport 與 footer
shortcuts；`--minimal` 與 `--no-alt-screen` 則是 scrollback-native rendering。這個
frontend 先把 shell 的 layout、streaming 與 approval card 做成可驗證的 screen
model，再逐步增加 panel、picker 與 richer composer。
