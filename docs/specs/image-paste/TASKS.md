---
id: tasks-lumen-image-paste
title: Lumen image paste - tasks
status: done
created: 2026-10-15
updated: 2026-10-15
spec: spec-lumen-image-paste
---

<!-- markdownlint-disable MD025 -->
# Tasks
<!-- markdownlint-enable MD025 -->

規則：每個 task 先寫 failing test（Red），再最小實作（Green）。Test ID 對應 `TESTS.md`。

- [x] T0 probe（ACP `promptCapabilities.image`、codex `localImage` schema），寫 SPEC / TASKS / TESTS / PROGRESS
- [x] T1 `Backend.Prompt` 加 `images`、`Capabilities.ImagePrompt`；更新所有 implementation / fake / 呼叫端 — T-IMG-16
- [x] T2 ACP：initialize 解析 image capability、prompt image block、gating — T-IMG-11..13
- [x] T3 Codex：`localImage` input item、`ImagePrompt=true` — T-IMG-14
- [x] T4 chip 與 attachment：插入、Backspace / Delete 整段刪除、送出收集、queue — T-IMG-07..10
- [x] T5 clipboard reader（可注入）+ Ctrl+V 流程 + fallback — T-IMG-01..06
- [x] T6 送出 / 入列的 unsupported backend 檢查 — T-IMG-15
- [x] T7 預覽：focus 規則、caption、kitty escape（transmit once / place / delete）、renderScreen 整合 — T-IMG-17..23
- [x] T8 temp 目錄清理、lifecycle 結束時刪圖
- [x] T9 驗收：`go vet ./...`、`go test -race -count=1 ./...`、`go build ./cmd/lumen`、tmux 手動 smoke
