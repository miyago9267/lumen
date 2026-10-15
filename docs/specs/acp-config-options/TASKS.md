---
id: tasks-lumen-acp-config-options
title: Lumen ACP configOptions-driven UI - tasks
status: done
created: 2026-10-15
updated: 2026-10-15
spec: spec-lumen-acp-config-options
---

<!-- markdownlint-disable MD025 -->
# Tasks
<!-- markdownlint-enable MD025 -->

規則：每個 task 先寫 failing test（Red），再最小實作（Green），結束時 `go test -race ./...` 必須綠。Test ID 對應 `TESTS.md`。

- [x] T0 probe 真實 adapter（initialize + session/new），寫 SPEC / TASKS / TESTS / PROGRESS
- [x] T1 fake agent：可寫入的 mode / effort / style、`FAKEACP_EXTRA`、boolean option
- [x] T2 backend：`ConfigOption`、`EvConfigOptions`、`SetConfigOption`、generic 解析與套用（取代 model / effort 特例）— T-CFG-01, 06..09, 20, 21, 23, 26, 27
- [x] T3 UI：catalog 與 precedence、`/<id>` handler、`/model` 走 generic — T-CFG-02..05, 12..14
- [x] T4 UI：參數補全 generalize + mode 安全 — T-CFG-10, 11, 25
- [x] T5 顯示：header / dock / Coralline / `/status`、event 重整 — T-CFG-15..19, 22, 24
- [x] T6 移除 orphan（`ModelSetter`、`ModelSwitch`、`acpModels`…），更新舊測試與 backend-adapter SPEC
- [x] T7 驗收：`go vet ./...`、`go test -race -count=1 ./...`、`gofmt -l .`
