---
id: progress-lumen-transcript-ux
title: Lumen transcript UX cleanup - progress
status: done
created: 2026-10-15
updated: 2026-10-15
spec: spec-lumen-transcript-ux
---

<!-- markdownlint-disable MD025 -->
# Progress
<!-- markdownlint-enable MD025 -->

## 狀態

| Item | 狀態 | 測試 |
| --- | --- | --- |
| 1 assistant 拆段 | 完成（not a Lumen bug，行為未改） | T-UX-01、T-UX-02 |
| 2 placeholder | 完成 | T-UX-03..07 |
| 3 tool marker | 完成 | T-UX-08..10 |
| 4 inline code | 完成 | T-UX-11..14 |
| 5 turn 底部 | 完成 | T-UX-15..20 |
| 6 card 框線 | 完成（只留 header rule） | T-UX-21 |

## 驗收（2026-10-15）

- `go vet ./...`、`go test -race ./...`、`gofmt -l .` 皆乾淨（見最終回報）。
- 既有測試因文案 / 框線改動而更新：`TestScreenCollapsesToolOutputByDefault`、
  `TestScreenHistoryToolOutputUsesCollapsedBlock`、`TestCompletedToolActivityCollapsesIntoOneHiddenRow`
  （marker 文字）、`TestCodexBackendTurnRendersTranscript`、`TestToolCompletionLabelIsShownVerbatim`、
  `TestFailedTurnShowsReason`、`TestTurnFinishAddsRecap`（recap 一行）、
  `TestScreenInputAndOutputShareTurnCardWithSeparateColors`、`TestScreenTurnUsesFlatRoleRowsWithHoverDetail`
  相鄰的 card 斷言、`TestScreenTurnCardExposesAskAndForkActions`（header rule）。
- `docs/specs/lumen/SPEC.md` 已更新 Turn cards、Low-density transcript、Coralline payload 三處文字。

## 未驗證

- 沒有以真實 TTY 手動操作 full-screen（hover 反白、click 展開、顏色實際觀感）；只以 `screenFrame` / row 層級測試與
  一次 preview frame 輸出檢查。
- item 1 沒有抓真實 adapter wire log；「還」單獨成段是 model/adapter 的 content block 切法，Lumen 端只證明不會拆連續 chunk。
- 以真實 `statusline.sh` 餵 `"model":{}` 的 payload 可正常渲染（手動確認一次，無自動測試）。
- ACP effort 的 `thought_level` option 依 2026-10-15 probe 的 `effort` option 實作，fake agent 驗證；真實 adapter 的
  `config_option_update` 是否會在 effort 改變時推送未驗證。
