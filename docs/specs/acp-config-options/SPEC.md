---
id: spec-lumen-acp-config-options
title: Lumen ACP configOptions-driven UI
status: implemented
created: 2026-10-15
updated: 2026-10-15
author: Miyago
approved_by:
tags: [lumen, tui, acp, config-options, slash-commands]
priority: medium
depends_on: [spec-lumen, spec-lumen-backend-adapter, spec-lumen-transcript-ux]
---

<!-- markdownlint-disable MD025 -->
# Lumen ACP configOptions-driven UI
<!-- markdownlint-enable MD025 -->

> 狀態：implemented（2026-10-15）。範圍只含 ACP backend 的 `configOptions` 處理、slash command catalog、
> 參數補全、header / dock / Coralline 顯示與 `/status`。Codex native path 不變。

## Goal

UI 跟著 ACP backend 宣告的 `configOptions` 走：adapter 新增一個 `type: "select"` 的 option，Lumen 不改 code
就多一個 `/<option id>` command（列出、切換、Tab 補全）。過去只有 `model` 被 hardcode
（`acpModelsFromConfig`、`SetModel`、`Caps().ModelSwitch`、`/model` 專屬 handler / 補全），effort 與 mode
只是顯示用途。

## Non-goals

- 不處理非 `select` 的 option（`boolean` 等；ACP schema 目前只有 select 與 unstable boolean）。
- 不改 Codex backend：`/model` 仍走 `ModelPicker`（`model/list`）與 per-turn override。
- 不實作 legacy `session/set_mode`（`modes` API）。只有 `modes` 沒有 mode configOption 的 agent，mode 仍只靠
  既有 permission 警示呈現，沒有 `/mode`。
- 不自動切換任何 option（尤其 mode category）；不新增 retry / 預設值寫入。

## Probe：真實 adapter（2026-10-15）

`claude-agent-acp@0.90.0`（`agentInfo.version`），只送 `initialize` + `session/new`（沒有 prompt，不花額度；
probe process 已 kill，確認無殘留）。不帶 `_meta`，所以 mode 清單含 `bypassPermissions`；Lumen 實際送
`allowDangerouslySkipPermissions:false`（見 backend-adapter SPEC），該 mode 不會出現。

`session/new` result keys：`sessionId, modes, configOptions`（沒有 `models`）。`configOptions`（順序即 adapter 順序）：

| id | category | type | currentValue | 選項數 | 備註 |
| --- | --- | --- | --- | --- | --- |
| `mode` | `mode` | select | `auto` | 5 | `default, acceptEdits, plan, auto, bypassPermissions`；name 為 Manual / Accept edits / Plan / Auto / Bypass permissions |
| `model` | `model` | select | `opus` | 13 | flat（無 group）；清單依帳號而定 |
| `effort` | `thought_level` | select | `medium` | 6 | `default, low, medium, high, xhigh, max` |
| `fast` | `model_config` | select | `off` | 2 | `on, off`；新的、過去 Lumen 完全看不到的 option |
| `agent` | （無 category） | select | `default` | 12 | 選項 name 等於 value、description 很長（含換行 / CJK） |

觀察：(1) 所有 option 都是 `select`；(2) `category` 可省略（`agent`）；(3) 每個 option 帶 `name` 與 `description`；
(4) `mode` option 的 `currentValue` 與 `modes.currentModeId` 相同。`fast`、`agent` 在這個設計下自動變成
`/fast`、`/agent`，不需要任何 Lumen 變更。

## Decisions

### D1 資料模型與 interface

- `ConfigOption{ID, Name, Description, Category, Current, Values []modelOption}` 是 backend-neutral 形狀
  （`modelOption` 沿用為通用的 select value：`ID`=value、`Name`、`Description`、`Current`）。
- backend 只多實作一個 optional interface：`ConfigSetter.SetConfigOption(sessionID, id, value) (string, error)`，
  回傳 backend 認可的 value。**移除** `ModelSetter` 與 `Capabilities.ModelSwitch`（只剩 ACP 使用；codex 的
  `ModelSwitch:true` 沒有實際作用）。`ModelPicker` / `Capabilities.ModelList` 保留給 codex 與 `capsSummary`。
- ACP backend 移除 `acpModels`、`acpModelsFromConfig`、`acpEffortOf`、`ModelOptions`、`SetModel`、`b.models`、`b.effort`。

### D2 Events：一個 generic event

`EvConfigOptions{Options []ConfigOption}` 取代 `EvModelChanged` 與 `EvEffortChanged`（兩者刪除）。
理由：兩個 per-category event 只是同一件事的切片，新增 category 就要再加 event；UI 反正要存完整清單
（catalog、`/status`、補全都要）。`EvModeWarning` 保留（permission 警示是獨立的 safety 規則）。

- 發送時機：`session/new|load|resume` 結果有可用 option 時一定送；`config_option_update` 與
  `session/set_config_option` 回應在清單內容改變時送。
- 語意是整份取代（ACP 的 update 與 set 回應都帶完整 `configOptions`），所以 option 消失（例如換到不支援 effort 的
  model）時，UI 的 effort 也消失，不留舊值。
- `current_mode_update`（legacy）同步更新清單中 mode category option 的 current 並送 `EvConfigOptions`。
- 沒有 option 的 session 結果（`session/load` 回 `null`）沿用先前清單，行為同過去 model 清單。
- Legacy `models.availableModels`（沒有 model category option 時）：合成 `ConfigOption{ID:"model",Category:"model"}`，
  切換仍用 `session/set_model`；保留既有 fallback，不另開路徑。

### D3 Catalog 與 precedence

dynamic catalog（ACP）= Lumen-local > configOption command > agent `available_commands`。

- Lumen-local = `localSlashCommands` 與其 alias。option id（小寫）與 local 名稱 / alias 相同時，該 option 不產生
  command（仍出現在 `/status`、header）。
- option id（小寫）與 agent command 同名時，option 勝出，agent command 被略過。
- 兩個 option id 小寫後相同：第一個勝出。
- 沒有 `Values` 的 option（例如 fake adapter 的空 mode）不產生 command。
- `/model` 不再有特例：ACP 的 `/model` 就是 id=`model` 的 option command；描述由 option 的 `name` / `description` 組成。
- 已知碰撞（真實 adapter）：沒有。`agent` 與 Codex 靜態 catalog 的 `/agent` 同名，但靜態 catalog 只用在 codex，
  不會同時出現。

### D4 指令行為

- `/<id>` 或 `/<id> list`：列出全部 value，目前值以 `* ` 標記，格式 `* <value> · <name> · <description>`
  （name 與 value 相同時省略 name）。
- `/<id> <query>`：整段 args（trim 後）當 query。比對順序：(1) value 完全相同；(2) value 或 name 不分大小寫相同。
  第 2 步命中多個不同 value 時回 `ambiguous`；沒有命中回 `unknown <id> value "x"; available: a, b, c`
  （此時不送 request）。命中後送 `session/set_config_option{sessionId, configId, value}`，以回應的完整
  `configOptions` 更新清單。agent 錯誤原樣顯示，current 不變。成功訊息 `<id> · <value>`。
- 與過去 `/model` 的差異：query 用整段 args（過去只取第一個字），所以 `/mode Accept edits` 可行。

### D5 參數補全

`/<id> ` + 部分字串：popup 列出該 option 的 value（filter 比對 value 與 name，現值標 `(current)`），Tab 補成
`/<id> <value>`，Enter 行為同過去 `/model`（部分輸入時補全選取項再執行；空白時列出）。title：model 沿用
`⌘ models`，其他 `⌘ <id> values`。候選來源：config option（無 RPC，直接讀 UI 的清單）；codex 的 `/model`
仍用 `ModelPicker` 並保留 per-(backend, session) 快取（避免每次按鍵一個 RPC）。

### D6 顯示順序

header、dock、Coralline 依 category 固定順序：model、`thought_level`（effort）、mode。找不到某個 category 就省略
（沿用 0.5.0 的 unknown-omitted 規則）。

- 有 config option 清單時，model / effort 只取自清單（不 fallback 到 session 開啟時的舊值，也忽略 codex 專屬的
  per-turn override）；沒有清單時維持過去 `override > thread` 行為。
- header：`model · effort · mode · approval · sandbox`。
- dock：`model (effort) · mode · approval`。已有 `permissions: <mode> (from agent settings)` 警示時不重複顯示 mode。
- Coralline：新增選填 `mode: {"id": "<value>"}`（未知時省略）；`model.display_name`、`effort.level` 不變。
  consumer 看不認得的 key 無影響。

### D7 `/status`

在既有欄位之後加一段 `options`，列出每個 config option 的 `<id>  <current value>  (<name>)`，含沒有 command 的
（被 local 蓋掉或沒有 value 的）。沒有 config option 時不輸出該段。

### D8 Safety

- `session/set_config_option` 只由使用者輸入的 `/<id> <value>` 觸發；Lumen 任何自動流程都不呼叫。
  這**修改** backend-adapter SPEC 的原句「不送對 mode/permission 類 `session/set_config_option`」：改為
  「Lumen 不自動送；使用者明確下 `/mode <value>` 才送」。`allowDangerouslySkipPermissions:false` 限縮不變，
  所以 `/mode` 選不到 `bypassPermissions`（adapter 不列出）。
- mode category 的參數 Enter 不做「部分輸入補全後直接執行」：`/mode ac` + Enter 只補成 `/mode acceptEdits`，
  需要再按一次 Enter。其他 category 維持過去 `/model` 行為。
- 切到非 default mode 後，既有 `EvModeWarning`（`permissions: <mode> (from agent settings)`）照常持續顯示；
  切回 `default` 清除。permission request 仍 never auto-allowed（approval card 不受影響）。

## 對 `docs/specs/backend-adapter/SPEC.md` 的影響

- capability 表：`ModelSwitch` 列刪除；`ModelList` 說明改為「有 model category 的 select option」。
- Security 的「不送 mode/permission 類 `session/set_config_option`」改為 D8 的版本。
- `unverified`：真實 adapter 對 `fast`、`agent`、`mode` 的 `session/set_config_option` 沒有實測（只實測過 `model`），
  實測會改變 session 狀態甚至 spawn agent，本輪不做。

## Test strategy

Red -> Green；fake ACP agent 新增 `style`（Lumen 不認得的 select option，由 `FAKEACP_EXTRA=1` 開啟）、
可寫入的 mode / effort / style 狀態與一個 `boolean` option（必須被忽略）。`style` 的存在就是「新 option 不需要
任何 Lumen code 變更」的證明。Test ID 見 `TESTS.md`。
