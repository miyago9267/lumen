---
id: progress-lumen-acp-config-options
title: Lumen ACP configOptions-driven UI - progress
status: done
created: 2026-10-15
updated: 2026-10-15
spec: spec-lumen-acp-config-options
---

<!-- markdownlint-disable MD025 -->
# Progress
<!-- markdownlint-enable MD025 -->

## 狀態

| Req | 狀態 | 測試 |
| --- | --- | --- |
| 1 select option -> `/<id>`（列出 / 切換 / 補全） | 完成 | T-CFG-01..11 |
| 2 catalog precedence | 完成 | T-CFG-12..14 |
| 3 header / dock / Coralline / `/status` | 完成 | T-CFG-15..19 |
| 4 generic refresh | 完成（`EvConfigOptions` 取代 `EvModelChanged` / `EvEffortChanged`） | T-CFG-20..24 |
| 5 safety | 完成 | T-CFG-25..27 |
| 6 codex 不變 | 完成 | T-CFG-28 |

## Probe（2026-10-15）

真實 `claude-agent-acp@0.90.0`，`initialize` + `session/new`，無 prompt；probe process 已 kill、確認無殘留。
option ids：`mode`(mode)、`model`(model)、`effort`(thought_level)、`fast`(model_config)、`agent`（無 category），全為 `select`。
實作完成後以真實 adapter 做過一次 backend + UI 層確認（同樣只有 `session/new`）：`SessionInfo` = `opus` / `medium`，
`ConfigOptions()` 五個 option，catalog 出現 `/mode`、`/model`、`/effort`、`/fast`、`/agent`；Lumen 的
`allowDangerouslySkipPermissions:false` 使 `mode` 只有 4 個 value（無 `bypassPermissions`）。

## 實作紀錄

- 刪除：`ModelSetter`、`Capabilities.ModelSwitch`、`EvModelChanged`、`EvEffortChanged`、`acpModels`、`acpModelsFromConfig`、
  `acpEffortOf`、`acpBackend.ModelOptions/SetModel/setModelsLocked`、`modelArgInput/modelArgItems/exactModelArg`、
  `slashCatalog` 內 model 特例。
- 新增：`ConfigOption`、`ConfigSetter`、`EvConfigOptions`、`ConfigOption.Resolve`、`acpBackend.SetConfigOption/ConfigOptions`、
  `native_command_config.go`（`/<id>` handler、`/status` options 段）、`commandArgItems`（generalized 補全）。
- 舊測試：`model_command_test.go`、`backend_acp_model_test.go` 改寫為 codex-only / generic；`TestACPEffortFromConfigOptions`、
  `TestEffortChangedUpdatesHeader`（T-UX-06/07）改用 `EvConfigOptions`；`TestACPModeFromConfigOptions` 改吃 `[]ConfigOption`。
- fake agent：mode 有 value 且可寫入、`FAKEACP_EXTRA=1` 加 `style`（未知 option）與 boolean `brief`、`configpush` / `effortdrop` scenario。

## 未驗證

- 真實 adapter 對 `fast` / `agent` / `mode` 的 `session/set_config_option` 沒有實測（會改變 session 狀態，`agent` 甚至會換 persona）；
  只有 fake agent 與先前對 `model` 的實測。
- live TUI 手動操作（`/effort high`、`/mode ` + Tab）沒有跑，只有 unit / fake-adapter 覆蓋。
- 真實 adapter 在換 model 後 effort option 是否消失 / 改變 value 清單，未觀察；行為依整份取代語意處理（T-CFG-20、22）。

## 驗收（2026-10-15）

見最終回報（`go vet ./...`、`go test -race -count=1 ./...`、`gofmt -l .`）。
