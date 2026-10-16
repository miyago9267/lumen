---
id: tests-lumen-image-paste
title: Lumen image paste - acceptance tests
status: done
created: 2026-10-15
updated: 2026-10-15
spec: spec-lumen-image-paste
---

<!-- markdownlint-disable MD025 -->
# Acceptance tests (EARS)
<!-- markdownlint-enable MD025 -->

Kind：U = unit、I = integration（fake ACP agent / piped codex）。以 `go test -race ./...` 執行。

## Req 1：clipboard reader 與 Ctrl+V

- **T-IMG-01 (U)** WHEN Ctrl+V and the injected reader returns an image, the UI shall insert `[Image #1]` at the cursor,
  record the attachment (path, mime, size, width, height), and number the next paste `#2`.
- **T-IMG-02 (U)** WHEN the reader returns no image but text, the UI shall insert the text at the cursor like a paste.
- **T-IMG-03 (U)** WHEN the reader returns neither image nor text, the UI shall show muted `clipboard has no image` and
  leave input unchanged.
- **T-IMG-04 (U)** WHEN the backend lacks `ImagePrompt` and the clipboard holds an image, the UI shall show muted
  `<backend> does not accept images`, insert nothing, and remove the temp file it created.
- **T-IMG-05 (U)** WHEN the reader fails, the UI shall show muted `clipboard unavailable: <err>` and leave input unchanged.
- **T-IMG-06 (U)** The Finder-file helper shall map png/jpg/jpeg/gif/webp extensions to a mime and reject others;
  mime detection shall follow content sniffing (a PNG named `.jpg` is `image/png`; a text file is rejected).

## Req 2：chip 與 attachment

- **T-IMG-07 (U)** WHEN Backspace is pressed on or right after a chip with an attachment, the UI shall remove the whole
  chip and place the cursor at its start; a literal `[Image #9]` without attachment shall delete one rune only.
- **T-IMG-08 (U)** WHEN Delete is pressed at the start of or inside a chip, the UI shall remove the whole chip;
  otherwise it shall delete the rune under the cursor.
- **T-IMG-09 (U)** WHEN submitting, the UI shall pass to the backend only attachments whose chip still appears in the
  text (in order, once each), and the prompt text shall still contain the chips.
- **T-IMG-10 (U)** WHEN a prompt with a chip is queued while busy, the queued prompt shall carry its image when it
  starts.

## Req 3：backend

- **T-IMG-11 (I)** WHEN the ACP agent advertises `promptCapabilities.image` and `Prompt` has images, `session/prompt`
  shall contain the text block followed by `{"type":"image","mimeType","data"}` blocks (base64 of the file).
- **T-IMG-12 (I)** WHEN the agent does not advertise image support, `Caps().ImagePrompt` shall be false and `Prompt`
  with images shall return an error without sending `session/prompt`.
- **T-IMG-13 (I)** WHEN `initialize` returns `promptCapabilities.image: true`, `Caps().ImagePrompt` shall be true.
- **T-IMG-14 (I)** WHEN codex `Prompt` has images, `turn/start.input` shall be the text item followed by
  `{"type":"localImage","path":...}` items; `Caps().ImagePrompt` shall be true.
- **T-IMG-15 (U)** WHEN the text contains a chip with an attachment and the backend lacks `ImagePrompt`, submit shall
  return an error, send no prompt, and keep the input.
- **T-IMG-16 (U)** WHEN submitting text with chips on an image-capable backend, the backend shall receive the matching
  `promptImage{Path, MIME}` list.

## Req 4：preview

- **T-IMG-17 (U)** WHILE in screen mode, the focused chip shall be the one containing the cursor in `(start, end]`,
  else the one starting at the cursor; none otherwise (no attachment, inline mode, approval/dashboard open).
- **T-IMG-18 (U)** Kitty graphics detection shall be true for `TERM_PROGRAM` ghostty/kitty/WezTerm and `TERM` containing
  kitty/ghostty, false for tmux and unknown terminals.
- **T-IMG-19 (U)** The kitty escape generators shall emit chunked transmit (`a=t,f=100,i,q=2`, `m=1` until last chunk,
  chunks <= 4096), place (`a=p,i,p=1,c,r,C=1,q=2`) and delete (`a=d`) sequences.
- **T-IMG-20 (U)** WHILE a chip stays focused across several `renderScreen` calls, the image shall be transmitted once
  and placed on each render; WHEN focus leaves, a placement delete shall be emitted; WHEN the UI exits, all transmitted
  images shall be freed.
- **T-IMG-21 (U)** WHEN no chip is focused, `screenFrame` shall be identical to the frame without attachments
  (same rows, gap row blank); WHEN a chip is focused, the gap row shall hold the caption.
- **T-IMG-22 (U)** The caption shall read `Image #N · WxH · PNG · 230 KB · <path>` and omit unknown fields.
- **T-IMG-23 (U)** The image cell box shall be at most 40x12, keep aspect (cell 1:2), and shrink to the available body rows.

## Req 5：transcript

- **T-IMG-24 (U)** WHEN a prompt with chips is submitted, the user block shall contain the text with the chips as-is.
