---
id: spec-lumen-transcript-ux
title: Lumen transcript UX cleanup (Claude ACP full-screen)
status: implemented
created: 2026-10-15
updated: 2026-10-15
author: Miyago
approved_by:
tags: [lumen, tui, ux, transcript, acp]
priority: medium
depends_on: [spec-lumen, spec-lumen-backend-adapter]
---

<!-- markdownlint-disable MD025 -->
# Lumen transcript UX cleanup
<!-- markdownlint-enable MD025 -->

> 狀態：draft（Miyago 看過 Claude ACP full-screen 截圖後列出的問題；本 spec 記錄問題、決策與驗收）。
> 觀察日：2026-10-15。範圍只含 full-screen transcript、header / dock、Coralline payload；
> 不改 backend protocol 行為，不加依賴。

## Goal

讓 full-screen transcript 在 Claude ACP backend 下更安靜、更準確：不顯示 placeholder、
每個 turn 的底部不重複、tool marker 與 inline code 容易讀、card 框線一致。

## Non-goals

- 不重做整體視覺，不改 Hybrid UX contract 的 Low-density transcript、turn card 與
  Coralline adapter 三個決策（只調整其呈現，見「對 `docs/specs/lumen/SPEC.md` 的影響」）。
- 不改 `--minimal` inline 模式的輸出（它本來就印原始文字，含 backtick）。
- 不新增 backend capability；ACP 的 approval 不從 mode 推導（見 D2）。

## 觀察到的問題與決策

### 1. Assistant 回覆被拆成兩段（結論：不是 Lumen bug）

截圖：`assistant › 還` -> tool marker -> `assistant › 沒驗證：…`，看起來像「還沒驗證」被切斷。

調查（live path）：`agent_message_chunk` -> `EvAssistantDelta`（`backend_acp.go` translateLocked）->
`ui.handleEvent` -> `screenAppend(screenAssistant, ...)`（`screen.go`）。`screenAppend` 在最後一個
entry 同為 assistant 且同 turn 時 append 到同一個 block；只有被別種 entry（tool、status、plan…）
隔開才開新 block。ACP 事件在 rpc read loop 內依線路順序同步處理，Lumen 不重排也不緩衝。

- 連續 chunk（無 tool 在中間）-> 一個 block：`TestContiguousACPChunksStayOneAssistantBlock`（fake agent 送
  `還` / `沒驗證：` / `go test 尚未跑` 三個 chunk，結果是單一 block）。
- `chunk -> tool_call -> chunk` -> 兩個 assistant block 夾一個 tool：`TestChunkToolChunkKeepsToolBoundaryOrder`。
  backend event 順序就是 `text | tool | text`，與 adapter 送出的順序一致。

**結論：** Lumen 對連續 chunk 沒有拆分；截圖中的順序是 adapter 實際送出的 text、tool、text，
Lumen 忠實呈現，故為 not a Lumen bug，不改行為。未能驗證的部分：沒有抓真實 adapter 的 wire log，
無法確認「還」單獨成為一段是 model 的輸出或 adapter 的 content-block 切法。
若日後要抓，用 `LUMEN_FAKEACP_LOG` 同款方式在真實 adapter 前加 tee 即可。

### 2. Placeholder 被當成值顯示

header、dock status label 與 Coralline `model.display_name` 會出現
`configured model · configured effort · configured approval · inherit`。

- **D2.1** `effectiveModel` / `effectiveEffort` / `effectiveApproval` / `effectiveSandbox` 未知時回傳空字串，
  不再回傳 placeholder。header、dock label 只串接非空 segment；全部未知時整段省略。
- **D2.2** Coralline payload 在 model 未知時 `model.display_name` 省略（JSON 為 `"model":{}`），effort 已是
  pointer + omitempty。payload 絕不含 `configured …` 或 `inherit`。
- **D2.3** ACP backend 的 model 來自 `session/new|load|resume` 與 `config_option_update` 的
  `category=model` option（既有 WIP 已處理）；effort 來自 `category=thought_level` 的 option
  （真實 adapter 0.90.0 的 `effort`，見 backend-adapter PROGRESS「/model 支援」），新增 `EvEffortChanged`。
- **D2.4** ACP 的 approval 不從 mode 推導：mode 非 default 時 dock 已有 `permissions: <mode>` 警示，
  再顯示 approval 會重複；default mode 沒有值得顯示的資訊。故 ACP 的 approval segment 省略。
  `/status`、`/permissions`、`/model` 這類明細畫面在未知時顯示 `unknown`（明細畫面需要明確欄位）。
- **D2.5** 只有 sandbox 沒有 thread 資料時（`sandboxLabel` 的 `inherit`）在 header / 明細畫面不再顯示。
  `sandboxLabel(nil)` 本身維持回 `inherit`（inline `--minimal` 狀態輸出與既有測試沿用）。

### 3. Tool marker 雜訊

每個 turn 都有 `status · tools hidden · 19 uses · click to show · Ctrl+O to show`，且 count 有時有有時沒有。

- **D3.1** marker 改成單行 muted：`⋯ 19 tools`（單數 `⋯ 1 tool`），沒有 `status ·` label，count 一律顯示。
- **D3.2** count 數的是 tool 使用次數（group 內非 `output` 的 tool block），不是 transcript entry 數
  （過去把 `$ cmd`、`output`、completion status 各算一次，所以與 recap 的 `47 tools` 不一致）。
  group 內沒有 tool block（例如只有 replay 的 file change status）時，每個 status 算一次。
- **D3.3** `click to show · Ctrl+O to show` 只留在 hover tooltip：`tool group · 19 tools · click or Ctrl+O to show`。
- 仍是一個 group 一行；click 與 `Ctrl+O` 展開行為不變。

### 4. Inline code 顯示成 `⟦…⟧`

- **D4.1** screen mode 的 code span 以顏色呈現（`cyan`，結束後回到該行原本的 role style），不再輸出
  `⟦` `⟧`。內部以兩個零寬 private-use 標記記錄 span，wrap 後才轉成 ANSI；輸入中若本來就含這兩個字元會先剝除，
  避免偽造。
- **D4.2** `NO_COLOR`（style 皆為空）時改輸出 backtick（`` `go test` ``）。`--minimal` 本來就印原始文字，不受影響。
- **D4.3** 複製 / selection：`copyText` 在彩色模式不含任何裝飾字元（也不含 backtick），NO_COLOR 模式含 backtick
  （因為畫面上就有，column 對得上）。
- **D4.4** wrap：span 整段寬度不超過可用寬度時，不跨行拆開（放不下就整段換到下一行）；
  超過可用寬度的 span 才會被拆，拆開處 close / re-open 讓每一行的顏色獨立正確。

### 5. Turn 底部重複

過去一個 turn 底部有 `info · recap · completed`、`47 tools`、`1 file change` 三行加 `status · turn completed`，
接著 `[ask]` 行、一條整列白底 bar、獨立的 `ask turn 3` composer，以及一行重複的 hint。

- **D5.1** recap 在 screen mode 收成一行 `✓ completed · 47 tools · 1 file change`（muted，無 `info ·` label；
  0 tools 時省略 tools 段；非 completed 用 `✗ <status>`），並移除 `status · turn completed`。
  新增 `screenRecap` block kind 承載。inline 模式的 recap / status 輸出不變。
- **D5.2** 白底 bar：調查結果是 hover highlight。滑鼠停在 card 底部 `└──┘` 或 `[ask]` 行時，
  `screenRenderedRow` 對整列做 reverse video（`highlightScreenRange(rendered, 0, width)`），
  hint line 同時出現 `hover · turn 3 · click to ask`，與截圖一致。不是獨立元件，但屬於 artifact：
  card chrome 本身不該被整列反白。決策：card chrome（header rule、action 行）hover 時只反白 action range
  （`[ask]` / `[fork]`）；沒有 action 的 chrome row 不反白；內容 row 與 tool marker 的 hover 行為不變
  （tool marker 只反白其文字寬度，不再整列）。
- **D5.3** 單一 ask affordance：turn 被選為 reply target 時，card 內的 `[ask]` 隱藏（composer 的
  `ask turn N` 即是唯一 affordance）；`[fork]` 照舊。沒有任何 action 時不輸出 action 行。
- **D5.4** hint line 去重：有 reply target 且 idle 時，status label 不再重複 `ask about turn N · type follow-up`
  （composer label 已標示），回到 model / effort 摘要；footer 改成 `Esc clear turn target`。已選取的 turn 的 hover
  tooltip 不再寫 `click to ask`。

### 6. Card 框線不一致

過去只有頂 `┌─ turn 3 ─┐` 與底 `└──┘`，內容 row 沒有左右框，只有 `[ask]` 行有右框。

- **D6.1**（lower-noise 選擇）只保留一條 header rule：`── turn 3 · selected ──────`，
  移除 `┐`、底部 `└──┘` 與 action 行的 `│`。理由：內容 row 已經靠 role label 排版，補齊四邊框會替每個 row
  增加兩欄 chrome 與縮窄 wrap 寬度，也會讓 copy offset 改變；只用 rule 分隔 turn，最後一個 turn 底下直接接 composer。
  welcome card（`try a prompt`）維持原樣。
- action 行改成縮排 `  [ask]  [fork]`（click range 起點不變，仍是欄 2）。

## 對 `docs/specs/lumen/SPEC.md` 的影響

- **Turn cards and action rail**：「包在同一張 card」改為「以 header rule 分隔的 turn 區塊」（D6.1）；
  click 追問與 `[fork]` 不變。
- **Low-density transcript**：「同一段只留一行 muted marker」不變；marker 文字格式改為 `⋯ N tools`（D3.1）。
- Coralline adapter：payload 在值未知時省略欄位（D2.2）；其餘不變。
- 兩處文字已同步更新，見該檔 `updated` 與對應 decision。

## Test strategy

Red -> Green -> Refactor，每個 item 先寫 failing test。測試放 `internal/lumen/transcript_ux_test.go`
（item 1 另用 fake ACP agent 新增 `split` / `interleave` scenario）。Test ID 見 `TESTS.md`。

## Open questions

- 真實 adapter wire log 抓取（item 1 的最後一哩）：需要 Miyago 提供一次重現的 session 或允許跑真實 adapter。
- ACP mode 是否該進 approval segment（D2.4 目前選擇省略）。
