---
id: tests-lumen-transcript-ux
title: Lumen transcript UX cleanup - acceptance tests
status: draft
created: 2026-10-15
updated: 2026-10-15
spec: spec-lumen-transcript-ux
---

<!-- markdownlint-disable MD025 -->
# Acceptance tests (EARS)
<!-- markdownlint-enable MD025 -->

Kind：U = unit、I = integration（fake ACP agent）、M = 手動。U/I 以 `go test -race ./...` 執行。

## Item 1：assistant chunk

- **T-UX-01 (I)** WHEN the ACP agent sends consecutive `agent_message_chunk` updates with no tool update between them,
  Lumen shall render them as exactly one assistant block with the concatenated text.
- **T-UX-02 (I)** WHEN the ACP agent sends chunk, tool_call, chunk, Lumen shall keep that order and render two
  assistant blocks separated by the tool block (the tool is the boundary; Lumen shall not merge across it).

## Item 2：placeholder

- **T-UX-03 (U)** WHEN model, effort, approval and sandbox are all unknown, the full-screen header shall not contain
  `configured` or `inherit`.
- **T-UX-04 (U)** WHEN some of model, effort and approval are unknown, the dock status label shall show only the known
  values and shall not contain `configured`.
- **T-UX-05 (U)** WHEN the model is unknown, the Coralline payload JSON shall not contain `configured` or `inherit`
  and shall not carry a `display_name`.
- **T-UX-06 (I)** WHEN the ACP session reports `category=thought_level` in `configOptions`, the session info shall carry that
  value as the effort, and a later `config_option_update` that changes it shall emit `EvEffortChanged`.
- **T-UX-07 (U)** WHEN `EvEffortChanged` arrives, the header shall show the new effort.

## Item 3：tool marker

- **T-UX-08 (U)** WHEN a tool group is collapsed, Lumen shall render one muted line `⋯ N tools` (`⋯ 1 tool` for one) whose
  text contains neither `click` nor `Ctrl+O` nor `status`.
- **T-UX-09 (U)** WHEN a group holds three tools with their outputs and completion statuses, the marker shall say `3 tools`.
- **T-UX-10 (U)** WHEN the pointer hovers the marker, the tooltip shall mention click and Ctrl+O; WHEN clicked or Ctrl+O is
  pressed, the group shall expand.

## Item 4：inline code

- **T-UX-11 (U)** WHEN an assistant line contains a code span in color mode, the rendered row shall contain no `⟦` or `⟧`
  and no backtick, and the span shall carry its own style sequence.
- **T-UX-12 (U)** WHEN colors are disabled (`NO_COLOR`), the rendered row shall show the span wrapped in backticks.
- **T-UX-13 (U)** WHEN the user copies a row containing a code span, the copied text shall be the original text without
  styling glyphs.
- **T-UX-14 (U)** WHEN a code span fits within the line width but not in the space left on the current line, Lumen shall move
  the whole span to the next line; WHEN it exceeds the line width, Lumen shall split it and keep each line's style balanced.

## Item 5：turn bottom

- **T-UX-15 (U)** WHEN a turn completes, the transcript shall contain one recap line `✓ completed · N tools · M file change(s)`
  and no `turn completed` status and no `info · recap` line.
- **T-UX-16 (U)** WHEN a turn failed or was interrupted, the recap line shall start with `✗` and name the status.
- **T-UX-17 (U)** WHEN the pointer hovers an action row or header rule, Lumen shall not reverse the whole row; only action
  ranges may be reversed.
- **T-UX-18 (U)** WHILE a turn is selected as the reply target, the card shall not show `[ask]` and the composer label shall
  name the turn; `[fork]` shall remain when forking is supported.
- **T-UX-19 (U)** WHILE a turn is selected and the session is idle, the dock shall not repeat `ask about turn` and its
  hover tooltip for that turn shall not say `click to ask`.
- **T-UX-20 (U)** WHEN there is no action to show, no action row shall be emitted.

## Item 6：card frame

- **T-UX-21 (U)** WHEN a turn renders, the first row shall be a header rule `── turn N ──…` without `┌` or `┐`, no row of
  the card shall contain `└`, `┘` or a `│` frame, and content rows shall use the full width.
