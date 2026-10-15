---
id: tests-lumen-acp-config-options
title: Lumen ACP configOptions-driven UI - acceptance tests
status: done
created: 2026-10-15
updated: 2026-10-15
spec: spec-lumen-acp-config-options
---

<!-- markdownlint-disable MD025 -->
# Acceptance tests (EARS)
<!-- markdownlint-enable MD025 -->

Kind：U = unit、I = integration（fake ACP agent）。以 `go test -race ./...` 執行。
fake agent 的 `FAKEACP_EXTRA=1` 會加上 Lumen 不認得的 `style` select option 與一個 `boolean` option。

## Req 1：select option -> slash command

- **T-CFG-01 (I)** WHEN `session/new` returns configOptions including an unknown-to-Lumen select option (`style`) and a
  boolean option, the backend shall emit one `EvConfigOptions` listing every select option in order and no boolean option.
- **T-CFG-02 (U)** WHEN the UI holds config options, the dynamic catalog shall contain one command per option that has
  values, named by the lowercased option id, with a description built from the option name / description.
- **T-CFG-03 (U)** WHEN an option is `style` (not special-cased anywhere), `/style` with no arg shall list its values with
  the current one marked `* `.
- **T-CFG-04 (U)** WHEN the user runs `/style loud`, the UI shall call `SetConfigOption(session, "style", "loud")`, show
  `style · loud` and use the new value as current.
- **T-CFG-05 (U)** WHEN `SetConfigOption` fails (unknown value), the UI shall surface the error containing the valid choices
  and keep the current value.
- **T-CFG-06 (I)** WHEN `SetConfigOption("style","loud")` is called, the backend shall send
  `session/set_config_option{sessionId,configId:"style",value:"loud"}`, apply the returned full configOptions and emit
  `EvConfigOptions` with the new current.
- **T-CFG-07 (I)** WHEN the value is unknown, the backend shall send no request and return an error listing the valid values.
- **T-CFG-08 (I)** WHEN the query matches a display name (case-insensitive) of exactly one value, the backend shall set that
  value; WHEN it matches several values, the backend shall return an `ambiguous` error and send no request.
- **T-CFG-09 (I)** WHEN the agent rejects `session/set_config_option`, the backend shall return the agent error and keep the
  previous current value.
- **T-CFG-10 (U)** WHEN the input is `/style ` plus a partial, the popup shall list matching values (current marked), Tab
  shall complete to `/style <value>` without running it.
- **T-CFG-11 (U)** WHEN the input is `/model ` on a codex-like backend (no config options), the existing ModelPicker popup,
  cache and per-turn override shall behave as before.

## Req 2：precedence

- **T-CFG-12 (U)** WHEN an option id equals a Lumen-local command (`status`), no option command shall be created and `/status`
  shall remain the local command.
- **T-CFG-13 (U)** WHEN an agent `available_commands` entry has the same name as an option, the catalog shall contain the
  option command once (option wins).
- **T-CFG-14 (U)** WHEN the backend reports a `model` option, `/model` shall be present and list / switch through the generic
  path; WHEN there is no model option, `/model` shall be absent from the dynamic catalog.

## Req 3：顯示

- **T-CFG-15 (U)** WHEN model, effort and mode are known, the header shall show them in the order model, effort, mode.
- **T-CFG-16 (U)** WHEN a category is unknown, the header, dock and Coralline payload shall omit it and shall not contain
  placeholders.
- **T-CFG-17 (U)** WHEN a permission warning is shown, the dock summary shall not repeat the mode.
- **T-CFG-18 (U)** WHEN the mode is known, the Coralline payload shall carry `mode.id`; WHEN unknown it shall carry no
  `mode` key.
- **T-CFG-19 (U)** WHEN `/status` runs, it shall list every config option (including ones without a command) with its
  current value.

## Req 4：refresh

- **T-CFG-20 (I)** WHEN `config_option_update` changes model, effort and style together, the backend shall emit exactly one
  `EvConfigOptions` carrying all three new values.
- **T-CFG-21 (I)** WHEN `session/resume` returns configOptions, the backend shall emit `EvConfigOptions` and report
  `Model` / `ReasoningEffort` in `SessionInfo`.
- **T-CFG-22 (U)** WHEN `EvConfigOptions` arrives without an effort option, the header shall drop the previous effort (no stale
  value).
- **T-CFG-23 (I)** WHEN the agent only reports legacy `models.availableModels`, the backend shall expose a synthesized
  `model` option and switch it with `session/set_model`.
- **T-CFG-24 (U)** WHEN the backend is switched, the UI's config options shall be cleared.

## Req 5：safety

- **T-CFG-25 (U)** WHEN the input is `/mode ac` and Enter is pressed, the UI shall complete to `/mode acceptEdits` without
  calling `SetConfigOption`; a following Enter shall run it.
- **T-CFG-26 (I)** WHEN `/mode plan` is applied and the agent reports a non-default mode, the backend shall emit
  `EvModeWarning`; WHEN it returns to `default` the warning text shall be empty.
- **T-CFG-27 (I)** WHEN a session opens, the backend shall send no `session/set_config_option` (only an explicit
  `SetConfigOption` call does).

## Req 6：codex

- **T-CFG-28 (U)** Existing codex tests (`TestModelCommandKeepsOverrideWithoutSetter`, `TestModelArg*` against a
  ModelPicker-only backend, `backend_codex_test.go`) shall pass unchanged in behavior.
