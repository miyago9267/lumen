---
id: tasks-lumen-transcript-ux
title: Lumen transcript UX cleanup - tasks
status: done
created: 2026-10-15
updated: 2026-10-15
spec: spec-lumen-transcript-ux
---

<!-- markdownlint-disable MD025 -->
# Tasks
<!-- markdownlint-enable MD025 -->

規則：每個 task 先寫 failing test（Red），再最小實作（Green），結束時 `go test -race ./...` 必須綠。
Test ID 對應 `TESTS.md`。

- [x] T0 寫 SPEC / TASKS / TESTS / PROGRESS
- [x] T1 調查 item 1：fake agent 加 `split` / `interleave` scenario，確認連續 chunk 不拆、tool 為邊界 — T-UX-01, T-UX-02（結論 not a Lumen bug，不改行為）
- [x] T2 item 2：placeholder 不顯示、Coralline payload 不含 placeholder、ACP effort 來自 configOptions — T-UX-03..T-UX-07
- [x] T3 item 3：tool marker `⋯ N tools`、count 一致、hint 只在 tooltip — T-UX-08..T-UX-10
- [x] T4 item 4：inline code 以顏色呈現、NO_COLOR 用 backtick、copy 乾淨、不跨行拆 — T-UX-11..T-UX-14
- [x] T5 item 5：recap 一行、移除 turn status、hover 不整列反白、單一 ask affordance、hint 去重 — T-UX-15..T-UX-20
- [x] T6 item 6：card 只留 header rule — T-UX-21
- [x] T7 同步 `docs/specs/lumen/SPEC.md` 兩處決策文字
- [x] T8 驗收：`go vet ./...`、`go test -race ./...`、`gofmt -l .`
