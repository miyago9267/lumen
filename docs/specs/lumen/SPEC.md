---
id: spec-lumen
title: Lumen terminal UI
status: in-progress
created: 2026-09-15
updated: 2026-09-16
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

本階段的 product direction 是 hybrid UX：輸入框以上採 Grok 式的 conversation
workspace 使用感；輸入框以下採 Claude Code 式的 compact session/control dock，
嵌入 Lumen 的 `carolline` identity。借用 interaction patterns，不搬運任何
provider 的品牌、畫面資產或 shortcut parity。

## Requirements

- 官方 `codex` binary、設定、session store 與既有 launcher 維持可用。
- 新入口必須能啟動本機 `codex app-server`，不把 credentials 放進 command
  arguments、logs 或 repo。
- 第一個 vertical slice 必須支援：new thread、resume thread、prompt
  input、assistant streaming、tool progress、approval request、tool user input
  與 interrupt。
- 預設採 full-screen app shell rendering：top status bar、中央 transcript、固定
  composer 與 footer shortcuts 必須同時可見。
- 新 thread 尚未送出 prompt 時，中央顯示 compact welcome card；不得因此增加
  app-server method、外部 dependency 或新的設定來源。
- full-screen transcript 必須支援 Lumen 自己繪製的滑鼠反白選取與 clipboard
  copy，複製結果不得帶出 ANSI 裝飾或 card 邊框。
- 每個 chat turn 必須 render 成獨立 turn card；card 內的 user input、assistant output
  與 tool/status rows 仍各自保留，以 user cyan / assistant green 區分內容。
- turn card 內的每個 user、assistant、tool、plan 或 status content block 都必須有
  自己的 inline label；user 與 assistant 不得共用同一個內層 block，且 role color
  與 label 必須在不依賴 ANSI 的情況下仍可由文字辨識。內層 block 預設維持低干擾
  排列，hover 或 selected 時才提供明顯的 visual emphasis 與 detail。
- full-screen turn card 必須支援 mouse click：點 card 內容可把 composer 指向該 turn
  追問，點 `[fork]` 可透過 `thread/fork.lastTurnId` 從該 turn 建立分支；drag selection
  仍保留原本的 clipboard copy 行為。
- `PageUp` / `PageDown` 與 alternate-screen mouse wheel 必須將 transcript viewport
  限制在頂端與底端之間；到達任一邊界後，繼續輸入不應改變 viewport。
- composer 的上下鍵必須先處理 prompt 首尾，再進入 history；`Alt+Enter` 與
  常見的 `Shift+Enter` 都能插入 newline，fullscreen cursor 不得佔用 input
  text 的 layout cell。
- idle 或 interrupted turn 的 `Ctrl-C` 必須在 2 秒內連按兩次才退出；第一次只顯示
  confirmation，busy turn 同時送出 interrupt。輸入單獨的 `exit` 可直接退出，不需要
  slash command。
- tool use 在完成後預設隱藏 command、output 與 completion status；同一段 tool
  activity 只留一行 muted marker，`Ctrl+O` 才展開。live reasoning 只顯示一個
  animated thinking 狀態，成功的 `completed exit 0` 不寫入 transcript。
- tool user-input request 的 options 必須逐列顯示；`↑/↓`、`1–9` 可選取，`isOther`
  允許直接輸入額外答案，free-text question 保留 newline。
- `--minimal` 與 `--no-alt-screen` 提供 inline/scrollback rendering，保留 terminal
  scrollback 與低干擾 fallback。
- UI 必須讓 model、reasoning effort、sandbox、approval policy、cwd 與 thread
  name 可見，不偷偷覆蓋 Codex config。
- app-server protocol version 必須與執行中的 `codex` 對齊；protocol failure
  要明確顯示並安全退出。
- `lumen` 與官方 `codex` 分開安裝、分開 rollback，更新官方 CLI 不得覆蓋
  custom frontend。
- 互動 composer 輸入 `/` 時，必須顯示與執行中官方 Codex TUI 對齊的 stable
  slash-command catalog；popup 必須支援 description、上下選擇、`Tab` completion、
  `Enter` execute 與 `Esc` close，且開始輸入 arguments 後不再誤顯示 command suggestions。
- catalog 內 command 必須維持 canonical name 與 alias；已知但尚未能由 Lumen 執行的
  command 必須顯示明確 fallback，不得把 slash text 當成普通 model prompt。
- `lumen <native-subcommand> ...` 與 `lumen codex ...` 必須將官方 Codex CLI 的
  stdin/stdout/stderr、arguments 與 exit code 交給原生 binary，並保留 Lumen 自己的
  `--help`、`--version` entrypoint。
- full-screen bottom status label 必須固定包含 `carolline`，並保留目前的 model、effort、
  approval 或 turn state。
- app-server EOF、transport error 或單一 UI event handler panic 不得直接結束 fullscreen
  session；transcript、composer 與明確的退出操作必須保留。
- app-server 斷線後，frontend 必須以 bounded backoff 嘗試重新啟動 app-server 並 resume
  目前 thread；重連期間畫面保持可見，重連失敗必須顯示狀態而不 busy-loop。

## Hybrid UX contract

### Layout boundary

Full-screen mode 的垂直順序固定為：

```text
┌──────────────────────────────────────────────┐
│ Upper conversation workspace                  │
│ transcript / turn cards / tool summary        │
│                                               │
│ Fixed composer                                │
├──────────────────────────────────────────────┤
│ carolline control dock                        │
│ session state / model / mode / attention      │
└──────────────────────────────────────────────┘
```

- Upper workspace 是主要閱讀區，保留 Grok 式的固定 prompt focus、bounded
  viewport、queue / steer、follow-up 與低干擾 tool summary。
- Composer 是唯一的 prompt input owner；transcript click、command popup、
  approval 與 user-input request 都只能改變 composer state，不得另開第二個
  input surface。
- `carolline control dock` 是下方固定的 session control surface，不是第二份
  transcript。它可以顯示 compact status、attention、queue count、connection
  state 與可用 shortcut。
- Dock 不得把 user / assistant message、tool output 或 reasoning 重新渲染一次；
  detailed content 一律留在 upper workspace 的 projection。
- Dock 預設維持 1–2 rows；窄 terminal 以 priority order 壓縮欄位，但固定保留
  `carolline`、connection / turn state 與目前可操作的 attention。

### Upper workspace interaction

- idle 時保持 transcript viewport 與 composer focus；新 thread 顯示 compact
  welcome card。
- busy 時 assistant stream、單一 transient thinking row、active tool marker
  與 queued prompt 都在 upper workspace 依 semantic block 顯示。
- completed tool activity 預設只產生一行 muted marker；command、output 與
  successful completion status 不進 default reading path。
- hover 只提供 block detail、role、tool group 或 action affordance；click、
  keyboard 與 screen-reader path 必須能在沒有 hover 的情況下完成相同操作。
- 使用者 scroll 離開 bottom follow 後，新 event 不得強制拉回；manual fold、
  selection、hover 與 follow state 各自保存。
- `Enter`、`Ctrl+Enter`、`Alt+Enter`、`Shift+Enter`、history、queue 與 interrupt
  的語意沿用現有 Lumen contract；hybrid layout 不新增第二套輸入模型。

### Lower carolline dock interaction

- stable：顯示 `carolline`、model、effort、approval、sandbox、cwd、thread name
  的可壓縮摘要。
- working：顯示 turn state、queued prompt count 與目前 attention；不複製
  assistant stream。
- needs input：顯示 approval 或 tool user-input 的可操作提示；完整問題與 options
  仍在 upper workspace / composer 呈現。
- reconnecting：顯示 bounded retry attempt 與下一次 retry state；transcript、
  composer 與 current view 保持可見。
- detached / fallback：顯示 view 是否仍 attached，以及 inline / scrollback
  fallback 是否啟用。
- Dock action 只派送既有 app-server 或 local UI action；不在 dock 內執行 tool、
  修改 permission default 或建立另一個 session。

### State invariants

- canonical session state、upper projection、composer state 與 dock projection
  必須分開；任何 hover、fold 或 dock compression 都不得改寫 canonical entries。
- 同一個 session 只能有一份 event ordering；fullscreen、inline、minimal 與
  accessibility mode 共享這份 ordering。
- terminal cleanup、app-server reconnect 與 renderer failure 不得清空 transcript
  或 draft；明確退出才可結束 frontend view。
- user、assistant、tool、plan、status block 必須可由文字 label 辨識；顏色只作
  reinforcement，不作唯一 role signal。

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
- **Lumen dark dock:** 保留暗色 status/composer 皮；每個 chat turn 以獨立 card 呈現，
  card 內的 user input 與 assistant output 各自保留 transcript row，以 cyan / green
  顏色區分，再搭配 event rail、compact footer 與 tooltip，避免複製 Grok 的畫面結構。
  - **Reason:** 借鑑互動層級，不複製品牌或 layout。
  - **By:** Miyago (2026-09-16)
- **Terminal-native editor:** prompt 以 rune buffer 管理；方向鍵支援游標與 prompt
  history，SGR mouse click 支援 composer cursor placement、turn action 與 selection。
  - **Reason:** 不引入大型 TUI dependency，維持低啟動成本與可測試 seam。
  - **By:** Miyago (2026-09-15)
- **Non-consuming fullscreen cursor:** fullscreen composer 只用 input text 計算 wrapping，
  terminal cursor 另外定位到 rune buffer 的游標位置。
  - **Reason:** visual cursor 不應偷吃一格，否則 input 剛好到邊界時會錯誤換行。
  - **By:** Miyago (2026-09-16)
- **Deterministic rich text:** Markdown fenced code、heading/list/quote 與常見
  Mermaid flowchart 由 frontend 做安全的 terminal preview，不執行 Markdown 或
  Mermaid 內容。
  - **Reason:** 渲染是 presentation-only，不應增加外部 process 或 network surface。
  - **By:** Miyago (2026-09-15)
- **Session supervisor:** `Ctrl+\\` / `/dashboard` 在同一個 pager 內呼叫
  `thread/list`，以 preview row 管理所有未封存的 top-level sessions；`1–9` /
  `Enter` resume，`n` 建立新 session，`r` refresh。
  - **Reason:** Grok 的核心差異是 pager 作為 process/session supervisor；先落一條真實
    app-server vertical slice，再擴充 background task、permission routing 與 pin/rename。
  - **By:** Miyago (2026-09-15)
- **Global resume picker:** `Ctrl+U` 與 `/resume`（不帶 thread id）開啟同一個 picker；
  `thread/list` 不帶 `cwd` filter，依 `updated_at` 顯示跨專案 sessions，選取後用
  `thread/resume` 重新載入該 session 的 history 與 cwd。
  - **Reason:** resume 需要跨 process、跨工作目錄找到持久化 session，不能只依賴目前
    啟動目錄或要求使用者手動輸入 thread id。
  - **By:** Miyago (2026-09-16)
- **Interruption semantics:** idle turn 的 `Enter` 送出；busy turn 的 `Enter` 排隊，
  `Ctrl+Enter` interrupt current turn 後接手；`/btw` 保留為 future independent aside
  turn，不把它偽裝成同一個 turn。
  - **Reason:** queue、cancel-and-send、aside 是三種不同意圖，不能共用一個插話行為。
  - **By:** Miyago (2026-09-15)
- **Application-owned selection:** full-screen mode 由 Lumen 接收 SGR mouse
  press/move/release，繪製 transcript selection，並透過小型 platform clipboard
  adapter 寫入系統 clipboard。
  - **Reason:** mouse tracking 仍可服務 scroll、hover 與 composer，而 selection
    不依賴 terminal emulator 是否接管 drag。
  - **By:** Miyago (2026-09-16)
- **Turn cards and action rail:** full-screen transcript 以 turn key 將 user prompt、
  assistant stream、tool progress 與 recap 包在同一張 card；card 內容 click 進入
  follow-up composer，`[fork]` action 使用該 turn 的 `lastTurnId`。
  - **Reason:** 保留 turn 邊界，讓追問與 branch 都有明確 target，同時不犧牲既有
    drag-to-copy selection。
  - **By:** Miyago (2026-09-16)
- **Low-density transcript:** tool activity 預設隱藏 command、output 與 completion
  status，同一段只留一行 muted marker；`Ctrl+O` 展開目前 transcript。reasoning 只
  由 transient status row 顯示，不保存每一個 reasoning item。
  - **Reason:** 預設把注意力留給 assistant conclusion，仍保留需要時的 debug path。
  - **By:** Miyago (2026-09-16)
- **Bounded transcript viewport:** `PageUp` / `PageDown` 與 mouse wheel 共用同一個
  scroll offset clamp，不能越過 transcript 的頂端或底端。
  - **Reason:** 到達 viewport 邊界後維持畫面，避免繼續滾動產生空白 transcript。
  - **By:** Miyago (2026-09-16)
- **Normalized Go layout:** entrypoint 放在 `cmd/lumen`，frontend implementation 與
  tests 放在 `internal/lumen`；未來 usage 與 custom command 功能先沿用 internal
  seam，等需求具體後再拆 package。
  - **Reason:** 消除 root `package main` 混合 entrypoint、app-server 與 UI 的結構，
    同時避免為 deferred features 建立空 abstraction。
  - **By:** Miyago (2026-09-16)
- **Interactive question selector:** user-input options 以單列 selector 呈現，預設
  選取第一項；`↑/↓` 和數字鍵切換，`isOther` 進入自訂答案輸入。
  - **Reason:** server request 的選項不能退化成一段不可操作的文字，且額外答案要有
    明確入口。
  - **By:** Miyago (2026-09-16)
- **Native command seam:** slash commands 由 canonical catalog 做 local completion，
  可直接承接的 lifecycle/read-only actions 呼叫 app-server 或 local read-only adapter；
  CLI subcommands 則 passthrough 到同一個官方 `codex` binary。
  - **Reason:** command name、description 與 native CLI 行為要保持原生語意，同時避免
    把不能執行的 slash text 送進 model；Lumen 的 interactive shell 仍保留自己的
    dashboard/help controls。
  - **By:** Miyago (2026-09-16)
- **Fixed status branding:** `carolline` 是 bottom status label 的固定文字，和 dynamic
  model/turn state 並列。
  - **Reason:** status bar 需要穩定可辨識的 embedded label，不把 branding 混進 Codex
    config 或 app-server state。
  - **By:** Miyago (2026-09-16)
- **Hybrid shell split:** composer 上方是 Grok-inspired conversation workspace，
  composer 下方是 compact `carolline` control dock；兩者只共享 canonical session
  state，不共享第二份 rendered transcript。
  - **Reason:** 把 Grok 的主要閱讀與輸入節奏，和 Claude-style 的 session attention
    / control visibility 放在各自清楚的 surface；Lumen 保留自己的 visual language。
  - **By:** Miyago (2026-09-16)
- **Grok-style upper interaction:** upper workspace 保留 fixed composer、bounded
  viewport、queue / steer、manual fold、hover affordance 與 explicit detail path。
  - **Reason:** 上半部應服務「讀結果、追問、控制目前 turn」，避免 status controls
    侵入主要 transcript。
  - **By:** Miyago (2026-09-16)
- **Claude-style embedded dock:** lower dock 只顯示 session、turn、attention、
  connection 與 runtime summary；approval / user-input 的詳細內容留在 upper
  workspace 與 composer。
  - **Reason:** 借用 Claude 的 compact status / session control 感，讓背景、重連、
    needs-input 與 fallback 狀態可見，又不產生第二個 conversation surface。
  - **By:** Miyago (2026-09-16)
- **Projection-first hybrid mode:** canonical event / transcript data 只保存一份，
  upper workspace 與 lower dock 各自從 session state 產生 projection。
  - **Reason:** hover、fold、dock compression、resume 與 renderer switch 不應互相
    污染，也讓 Go screen model 能在不啟動 real turn 的情況下測試。
  - **By:** Miyago (2026-09-16)
- **Role-scoped message blocks:** user、assistant 與每種 auxiliary content block 都使用
  自己的 inline label；外層 turn card 保留邊界，hover 或 selected 才強調目前 block。
  自然語句不以 punctuation 強制切分。
  - **Reason:** 讓對話來源一眼可辨，同時保留 Markdown、code fence 與 CJK 文字的原始
    結構，避免 resume history 被固定框線切碎。
  - **By:** Miyago (2026-09-16)
- **Recoverable session lifecycle:** app-server transport failure 只讓連線進入
  reconnecting state；UI event 與 render 邊界使用 recovery guard，避免單一 malformed
  event 把整個 TUI 帶走。
  - **Reason:** interactive session 的主要生命週期由 frontend 保持，Codex app-server
    仍是可替換的 backend process；斷線時不丟掉目前畫面與 composer。
  - **By:** Miyago (2026-09-16)

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
- [x] Phase 5b.2: user input/output 分離、hover tooltip 與 Lumen dark dock visual
  variant
- [x] Phase 5b.3: Markdown blocks、code blocks 與 Mermaid flowchart preview
- [x] Phase 5b.4: source、installer、spec 抽成 standalone repository
- [x] Phase 5b.5: fresh fullscreen thread 的 compact welcome card
- [x] Phase 5b.6: transcript selection/copy、editor boundary navigation 與
  low-density tool/reasoning rendering
- [x] Phase 5d: `cmd/lumen` + `internal/lumen` normalized Go layout
- [x] Phase 5b.7: user-input option selector、`isOther` custom answer 與 question
  composer navigation
- [x] Phase 5e: native Codex slash catalog、popup completion、common app-server command
  actions、CLI subcommand passthrough 與 `carolline` bottom status label
- [x] Phase 5f: role-scoped transcript blocks、app-server reconnect backoff 與 panic-safe
  fullscreen lifecycle
- [x] Phase 5b.8: per-turn cards、follow-up click target 與 turn-level fork action
- [x] Phase 5b.9: `Ctrl+U` resume picker、跨 cwd session list 與 history attach
- [ ] Phase 5c: fresh-context verification、per-tool expansion、session supervisor
  parity
  與 richer session controls
<!-- markdownlint-disable MD013 -->
- [x] Phase 6: hybrid Grok-above / Claude-carolline-below UX
  - [x] Phase 6.1: freeze upper workspace、composer 與 lower dock layout contract
  - [x] Phase 6.2: extract `TerminalLifecycle` single-owner setup / restore seam
  - [x] Phase 6.3: extract `SessionSupervisor` attach、single-flight reconnect、
    bounded backoff、resume / replay dedupe 與 cancellation
  - [x] Phase 6.4: define canonical semantic transcript entries and projections for
    upper workspace、lower dock、inline fallback 與 accessibility output
  - [x] Phase 6.5: implement per-tool `toolGroup` projection、hover affordance、
    click / `Ctrl+O` detail expansion 與 manual fold pin
  - [x] Phase 6.6: replace idle full-frame wake-up with dirty invalidation while
    retaining animation、resize 與 manual redraw timers
  - [x] Phase 6.7: run PTY、fake app-server、screen model、long-transcript and
    attended UX acceptance checks

目前 Phase 3 已有 approval、interrupt、CLI `/resume` 與明確的 model、reasoning、
sandbox、approval overrides；Phase 5b.9 已補上 interactive resume picker、跨 cwd
session list 與 history attach；Phase 5e 已加入 native command
catalog、popup 與 common command actions，但部分 Codex UI-only slash actions 仍只會
顯示明確 fallback。Phase 5a 已將 interactive default 改為 full-screen，`--minimal`
保留 inline fallback。

## Phase 6 implementation plan

### Module boundaries

| Slice | Primary files | Seam | First verification |
| --- | --- | --- | --- |
| Terminal stability | `screen.go`, `lifecycle.go` | `TerminalLifecycle` | PTY cleanup and redraw fixture |
| Session keep-alive | `app.go`, `session_supervisor.go` | `SessionSupervisor` | fake app-server EOF/restart/replay |
| Transcript model | `transcript.go`, `transcript_model.go` | canonical entry store | fixture projection and resume ordering |
| Hybrid rendering | `screen.go`, `ui.go`, `input.go` | upper/lower projection | fixed-size screen model snapshots |
| Tool disclosure | `transcript.go`, `screen.go` | `toolGroup` projection | hidden, hover, expand and copy tests |
| Render efficiency | `screen.go`, `ui.go` | dirty invalidation | idle and long-transcript counters |

The planned files are seams, not a request for a broad rewrite. The first implementation
may keep existing `ui` and `screenBlock` callers while moving ownership behind small
interfaces. A new type is justified only when it hides cleanup, replay, grouping or
projection behavior from those callers.

### Canonical state shape

The implementation must keep the following minimum information. Field names can follow
existing Go style, but the ownership and invariants are fixed:

- `SessionEntry`: `role`, `kind`, `turnID`, `blockID`, optional `parentID`, text or
  structured payload, lifecycle state and ordering metadata.
- `ToolGroup`: contiguous tool entries in one turn, group state, child entry IDs and
  user fold pin; it is derived projection state, not a replacement for tool entries.
- `ViewState`: `follow`, bounded `scrollOffset`, `hoverTarget`, selection range,
  `showToolDetail` and focused surface.
- `DockState`: connection state, turn state, attention kind, queue count, model,
  effort, approval, sandbox, cwd and thread label; it contains summaries only.
- `TerminalState`: raw mode, cursor, mouse, alternate screen and restore status;
  setup and cleanup must be idempotent.

Transient reasoning animation, hover, selection and retry countdown must not be persisted
as assistant or tool transcript content. Tool call and result metadata must remain
available to an explicit detail view and recovery replay.

### Render and interaction rules

- `screenFrame` reserves the lower dock before calculating upper transcript height; the
  dock never overlays composer or transcript rows.
- Upper rows are generated from canonical entries plus `ViewState`; lower rows are
  generated from `DockState`. No renderer reads raw app-server channels directly.
- Dock compression order is: keep `carolline` → connection / attention → turn state →
  model / effort → approval / sandbox → cwd / thread. Hidden fields remain available
  through `/status` or an explicit detail action.
- `toolGroup` is the only default aggregation boundary. A group cannot cross turns,
  pending approval, user-input request or error boundary.
- Hover changes emphasis or exposes a compact tooltip only. Detail expansion is also
  reachable by keyboard and click; mouse support remains optional when the terminal
  lacks tracking capability.
- Reconnect updates `DockState` and session ownership first; upper transcript and
  composer remain mounted while the client is replaced.

### Delivery gates

1. Terminal guard and redraw recovery pass before changing transcript data shape.
2. Supervisor fake tests pass before changing live reconnect orchestration.
3. Canonical entry fixtures render the current Lumen layout before adding new visual
   treatment.
4. Tool disclosure tests pass with default hidden detail and explicit expansion before
   adding richer dashboard controls.
5. A real PTY attended smoke confirms fresh thread, resumed thread, busy turn, blocked
   request, scroll-away, reconnect and clean exit.

### Rollback and safety

- Hybrid layout 只改 frontend projection；Codex model、app-server protocol、session
  store、sandbox、approval 與 MCP ownership 維持不變。
- `--minimal` 與 `--no-alt-screen` 必須繼續提供可用 fallback；dock overflow、
  unsupported mouse capability 或 renderer error 不得阻止 user 以 inline mode
  取回 session。
- Phase 6 不新增 permission default、不把 tool execution 移入 frontend，也不把
  provider-specific session data 寫入 Lumen 自己的 store。
- 若 hybrid renderer 的新 seam 未通過 gate，rollback 只停用 Phase 6 projection
  adapter，保留目前已驗證的 full-screen、hidden tool row 與 reconnect path。

### Planning assumptions

- Miyago 提到的 `coralline` 先對應 repo 既有 canonical label `carolline`；若是
  另一個 brand，需另開命名與 asset scope，不混入本次 UX implementation。
- 「輸入框以下」解讀為 composer 下方的固定 control dock；它不是第二份 transcript
  或第二個 prompt editor。

## Evidence

- [x] `go test -race ./...` passes。
- [x] binary build、`--help` 與 `--version` passes。
- [x] isolated installer test builds into a temporary `HOME` without touching the
  real `~/.local/bin`。
- [x] local one-shot smoke used `codex app-server` and returned streamed `READY`
  after `turn/completed`。
- [x] PTY smoke verified raw input、`/help`、`/status`、`Alt+Enter` 與 double-press
  `Ctrl-C`；plain `exit` 也會退出。
  `--continue` 也已 render persisted recent history。
- [x] fixed-size screen model tests verified top bar、transcript、CJK wrapping、固定
  composer、footer、shortcuts overlay 與 approval card。
- [x] app-server fixture tests verified approval routing、逐題 user-input response、
  secret-safe input rendering、permission empty grant 與 thread mismatch rejection。
- [x] `thread/tokenUsage/updated` fixture verified top bar context meter formatting。
- [x] PageUp/PageDown escape-sequence parsing、mouse-wheel handling 與 bounded
  transcript viewport verified。
- [x] app-server launch args include per-process `suppress_unstable_features_warning=true`；不
  修改 `CODEX_HOME/config.toml`。
- [x] PTY input model 與 screen tests verified mouse-wheel scroll events、one-row
  tool activity collapse、completion-status hiding、`Ctrl+O` toggle 與 compact turn
  recap。
- [x] unit tests verified prompt history、mouse cursor placement、user input/output
  separation、hover tooltip、Markdown/Mermaid preview、Dashboard preview 與
  queue/cancel-and-send parsing。
- [x] native command catalog、popup key handling、app-server command dispatch、CLI
  passthrough exit code 與固定 `carolline` status label tests。
- [x] unit test verified compact welcome rendering for a fresh fullscreen thread。
- [x] selection/copy、editor boundary navigation、reasoning animation 與 normalized
  package build evidence。
- [x] user-input options navigation、`isOther` custom answer 與 free-text newline
  evidence。
- [x] unit tests verified per-turn cards、follow-up click target、`[ask]` / `[fork]`
  routing 與 `thread/fork.lastTurnId` boundary。
- [x] `Ctrl+U` key parsing、global `thread/list`、跨 cwd `thread/resume` 與 resumed
  history rendering tests。
- [x] PTY tests verified prompt history、mouse cursor placement、Dashboard attach
  與
  Ctrl+Enter terminal encoding。
- [x] role block inline labels/colors、hover detail、app-server disconnect recovery、
  reconnect backoff 與 malformed event recovery 的 regression/PTY verification。
- [x] real PTY smoke verified app-server kill → `reconnecting` → `reconnected`
  and explicit `exit` with status 0；empty fresh threads fall back to a new thread
  in the same cwd when Codex reports `no rollout found`。
- [x] standalone repo builds and installs from its own `install.sh`; dotfile source
  copy is removed only after standalone verification。
- [x] Phase 6 unit slices verified idempotent terminal cleanup、canonical transcript
  projection、reconnect cancellation / replay dedupe、per-tool expansion、approval
  boundaries、scroll-away follow preservation 與 dirty event rendering。
- [x] fake app-server PTY smoke verified backend termination → `reconnecting` →
  `reconnected` → double `Ctrl-C` clean exit without losing the mounted composer。
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

## Phase 6 acceptance slice

以下項目全部通過前，Phase 6 不得標記完成：

- [x] fixed-size screen model 在正常 terminal height 下保留 upper workspace、
  composer 與 1–2 row `carolline` dock，三者不重疊。
- [x] upper workspace 維持 user / assistant 分離、Grok-style queue / steer、
  bounded scroll、manual fold、hover affordance 與既有 follow-up actions。
- [x] lower dock 在 idle、working、queued、needs-input、reconnecting、detached
  與 fallback 狀態顯示正確摘要，且不重複 transcript content。
- [x] completed tool activity 預設只顯示一行 marker；不同 turn、error、approval
  與 user-input boundary 不被錯誤合併；click / keyboard / `Ctrl+O` 可展開 detail。
- [x] app-server EOF、transport error、handler panic、resize failure 與 reconnect
  retry 不清空 transcript、composer 或 dock state；明確退出後 terminal 完整 restore。
- [x] inline、minimal、screen-reader projection 共享 canonical entries；copy 結果
  不含 ANSI、card border 或 dock metadata。
- [x] PTY、fake app-server、screen model、race、長 transcript benchmark 與 attended
  smoke 全部通過，且既有 app-server protocol / approval / sandbox tests 無回歸。

## Phase 6 planned files

- `internal/lumen/lifecycle.go` - `TerminalLifecycle` setup, restore and recovery seam
- `internal/lumen/session_supervisor.go` - app-server attach, reconnect,
  replay and cancellation owner
- `internal/lumen/transcript_model.go` - canonical `SessionEntry` and
  `ToolGroup` state, if existing transcript seam cannot hold it cleanly
- `internal/lumen/transcript.go`, `screen.go` - upper projection, tool disclosure,
  viewport and row grouping
- `internal/lumen/ui.go`, `input.go`, `turn_input.go` - dock actions, focus and
  existing queue / interrupt semantics
- `internal/lumen/*_test.go` - screen snapshots, projection fixtures, PTY and
  fake app-server acceptance tests
<!-- markdownlint-enable MD013 -->

## Files

- `cmd/lumen/main.go` - small executable entrypoint
- `internal/lumen/*.go`, `internal/lumen/lumen_test.go` - custom frontend source
  and tests
- `install.sh`, `uninstall.sh` - standalone local binary installer and rollback
- `README.md` - setup, interaction and rollback notes

## Notes

Grok Build 目前的 default shell 有固定 composer、transcript viewport 與 footer
shortcuts；`--minimal` 與 `--no-alt-screen` 則是 scrollback-native rendering。這個
frontend 先把 shell 的 layout、streaming 與 approval card 做成可驗證的 screen
model，再逐步增加 panel、picker 與 richer composer。

Native Codex command catalog、common command actions 與 per-turn card actions 已進入
normalized package；剩餘 UI-only slash actions、完整 command-specific picker 與
fresh-context verification 仍是後續 slice。
