package lumen

import (
	"encoding/json"
	"strings"
	"testing"
)

// transcript-ux 規格（docs/specs/transcript-ux）的測試。Test ID 對應 TESTS.md。

func replayEventsIntoScreen(t *testing.T, events []Event) *ui {
	t.Helper()
	frontend := &ui{screenMode: true, backend: &fakeBackend{}, cwd: "/tmp/project"}
	frontend.beginScreenTurn("")
	frontend.screenAdd(screenUser, "q")
	for _, event := range events {
		frontend.handleEvent(event)
	}
	return frontend
}

func assistantEntries(frontend *ui) []string {
	var texts []string
	for _, entry := range frontend.transcript.entries {
		if entry.kind == screenAssistant {
			texts = append(texts, entry.text)
		}
	}
	return texts
}

// T-UX-01：連續 chunk（中間沒有 tool）必須接成同一個 assistant block，不得拆成多個 message。
func TestContiguousACPChunksStayOneAssistantBlock(t *testing.T) {
	run := startFakeACP(t, nil, nil)
	sid := newFakeSession(t, run)
	frontend := replayEventsIntoScreen(t, run.runTurn(t, sid, "split"))

	got := assistantEntries(frontend)
	if len(got) != 1 || got[0] != "還沒驗證：go test 尚未跑" {
		t.Fatalf("assistant blocks = %q, want one joined block", got)
	}
}

// T-UX-02：adapter 實際送 text -> tool -> text 時，tool 是邊界，兩段文字各自成 block（不是 Lumen 的 bug）。
func TestChunkToolChunkKeepsToolBoundaryOrder(t *testing.T) {
	run := startFakeACP(t, nil, nil)
	sid := newFakeSession(t, run)
	events := run.runTurn(t, sid, "interleave")

	var order []string
	for _, event := range events {
		switch event := event.(type) {
		case EvAssistantDelta:
			order = append(order, "text:"+event.Text)
		case EvToolStarted:
			order = append(order, "tool")
		}
	}
	if strings.Join(order, "|") != "text:還|tool|text:沒驗證：" {
		t.Fatalf("backend event order = %q, Lumen must not reorder or merge across the tool", order)
	}

	frontend := replayEventsIntoScreen(t, events)
	if got := assistantEntries(frontend); len(got) != 2 || got[0] != "還" || got[1] != "沒驗證：" {
		t.Fatalf("assistant blocks = %q, want two blocks around the tool", got)
	}
	var kinds []screenBlockKind
	for _, entry := range frontend.transcript.entries {
		if entry.kind == screenAssistant || entry.kind == screenTool {
			kinds = append(kinds, entry.kind)
		}
	}
	if len(kinds) != 3 || kinds[0] != screenAssistant || kinds[1] != screenTool || kinds[2] != screenAssistant {
		t.Fatalf("entry kinds = %v, want assistant, tool, assistant", kinds)
	}
}

// --- item 2：placeholder ---

// T-UX-03
func TestHeaderOmitsUnknownValuesInsteadOfPlaceholders(t *testing.T) {
	frontend := &ui{screenMode: true, cwd: "/tmp/project", branch: "main"}
	header := stripANSI(frontend.screenHeaderLine(100))
	for _, placeholder := range []string{"configured", "inherit"} {
		if strings.Contains(header, placeholder) {
			t.Fatalf("header shows placeholder %q: %q", placeholder, header)
		}
	}

	frontend.thread.Model = "opus"
	frontend.thread.ReasoningEffort = "high"
	header = stripANSI(frontend.screenHeaderLine(100))
	if !strings.Contains(header, "opus · high") || strings.Contains(header, "configured") {
		t.Fatalf("header with known values = %q", header)
	}
}

// T-UX-04
func TestStatusLabelShowsOnlyKnownValues(t *testing.T) {
	frontend := &ui{screenMode: true, backend: &fakeBackend{}}
	if label := frontend.screenStatusLabel(); strings.Contains(label, "configured") || strings.Contains(label, "()") {
		t.Fatalf("empty status label = %q", label)
	}
	frontend.thread.Model = "opus"
	label := frontend.screenStatusLabel()
	if label != statusBarBrand+" · opus" {
		t.Fatalf("model-only status label = %q", label)
	}
	frontend.thread.ReasoningEffort = "high"
	frontend.thread.ApprovalPolicy = "on-request"
	if label := frontend.screenStatusLabel(); label != statusBarBrand+" · opus (high) · on-request" {
		t.Fatalf("full status label = %q", label)
	}
	frontend.thread.Model, frontend.thread.ReasoningEffort, frontend.thread.ApprovalPolicy = "", "", ""
	frontend.permissionNote = "permissions: auto (from agent settings)"
	if label := frontend.screenStatusLabel(); label != statusBarBrand+" · permissions: auto (from agent settings)" {
		t.Fatalf("note-only status label = %q", label)
	}
}

// T-UX-05
func TestCorallinePayloadOmitsUnknownModel(t *testing.T) {
	frontend := &ui{cwd: "/tmp/project"}
	encoded, err := json.Marshal(frontend.corallinePayload())
	if err != nil {
		t.Fatal(err)
	}
	for _, placeholder := range []string{"configured", "inherit", "display_name"} {
		if strings.Contains(string(encoded), placeholder) {
			t.Fatalf("payload carries %q: %s", placeholder, encoded)
		}
	}
	frontend.thread.Model = "opus"
	encoded, _ = json.Marshal(frontend.corallinePayload())
	if !strings.Contains(string(encoded), `"display_name":"opus"`) {
		t.Fatalf("payload lost the known model: %s", encoded)
	}
}

// T-UX-06
func TestACPEffortFromConfigOptions(t *testing.T) {
	run := startFakeACP(t, map[string]string{"FAKEACP_MODELS": "config", "FAKEACP_EFFORT": "high"}, nil)
	info, err := run.backend.NewSession(t.TempDir(), runtimeOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if info.ReasoningEffort != "high" || info.Model != "opus" {
		t.Fatalf("session info model/effort = %q/%q, want opus/high", info.Model, info.ReasoningEffort)
	}
	events := run.runTurn(t, info.ID, "effortpush")
	// events 先含 NewSession 時送出的那份（high），再來才是 update（xhigh）。
	var seen []string
	for _, event := range configEventsOf(events) {
		if effort, _ := findConfigOption(event.Options, "effort"); effort.Category == "thought_level" {
			seen = append(seen, effort.Current)
		}
	}
	if len(seen) != 2 || seen[0] != "high" || seen[1] != "xhigh" {
		t.Fatalf("effort in EvConfigOptions = %q, want [high xhigh]", seen)
	}
}

// T-UX-07：EvConfigOptions 更新 header 的 effort（見 config_options_ui_test.go 的 T-CFG-22）。
func TestEffortChangedUpdatesHeader(t *testing.T) {
	frontend := &ui{screenMode: true, branch: "main", thread: threadSummary{Model: "opus", ReasoningEffort: "high"}}
	frontend.handleEvent(EvConfigOptions{Options: []ConfigOption{
		selectOption("model", "Model", "model", "opus", "opus"),
		selectOption("effort", "Effort", "thought_level", "xhigh", "xhigh"),
	}})
	if header := stripANSI(frontend.screenHeaderLine(100)); !strings.Contains(header, "opus · xhigh") {
		t.Fatalf("header = %q", header)
	}
}

// --- item 3：tool marker ---

func collapsedMarkerRows(frontend *ui, width int) []screenTranscriptRow {
	var markers []screenTranscriptRow
	for _, row := range frontend.screenTranscriptRows(width) {
		if row.toolGroup != "" {
			markers = append(markers, row)
		}
	}
	return markers
}

func addToolUse(frontend *ui, id, title, output, failure string) {
	frontend.handleEvent(EvToolStarted{ToolID: id, Kind: ToolCommand, Title: title})
	if output != "" {
		frontend.handleEvent(EvToolOutput{ToolID: id, Text: output})
	}
	frontend.handleEvent(EvToolCompleted{ToolID: id, Status: ToolFailed, Label: failure})
}

// T-UX-08
func TestCollapsedToolMarkerIsShortAndQuiet(t *testing.T) {
	frontend := &ui{screenMode: true, backend: &fakeBackend{}}
	frontend.beginScreenTurn("t1")
	frontend.screenAdd(screenUser, "q")
	addToolUse(frontend, "a", "$ ls", "", "")

	markers := collapsedMarkerRows(frontend, 100)
	if len(markers) != 1 {
		t.Fatalf("marker rows = %d, want 1", len(markers))
	}
	plain := strings.TrimSpace(stripANSI(markers[0].rendered))
	if plain != "⋯ 1 tool" {
		t.Fatalf("single-tool marker = %q, want %q", plain, "⋯ 1 tool")
	}
	for _, noisy := range []string{"click", "Ctrl+O", "status", "hidden", "uses"} {
		if strings.Contains(plain, noisy) {
			t.Fatalf("marker text repeats %q: %q", noisy, plain)
		}
	}
}

// T-UX-09
func TestCollapsedToolMarkerCountsToolUsesNotEntries(t *testing.T) {
	frontend := &ui{screenMode: true, backend: &fakeBackend{}}
	frontend.beginScreenTurn("t1")
	frontend.screenAdd(screenUser, "q")
	addToolUse(frontend, "a", "$ one", "out one\n", "failed exit 1")
	addToolUse(frontend, "b", "$ two", "out two\n", "failed exit 2")
	addToolUse(frontend, "c", "$ three", "out three\n", "failed exit 3")

	markers := collapsedMarkerRows(frontend, 100)
	if len(markers) != 1 || strings.TrimSpace(stripANSI(markers[0].rendered)) != "⋯ 3 tools" {
		t.Fatalf("markers = %#v, want one `⋯ 3 tools`", markers)
	}
}

// T-UX-10
func TestToolMarkerHintLivesInTooltipAndStillExpands(t *testing.T) {
	frontend := &ui{screenMode: true, backend: &fakeBackend{}}
	frontend.beginScreenTurn("t1")
	frontend.screenAdd(screenUser, "q")
	addToolUse(frontend, "a", "$ ls", "a.txt\n", "failed exit 1")

	marker := collapsedMarkerRows(frontend, 100)[0]
	if !strings.Contains(marker.tooltip, "click") || !strings.Contains(marker.tooltip, "Ctrl+O") {
		t.Fatalf("tooltip = %q, want click and Ctrl+O hint", marker.tooltip)
	}

	frontend.toggleToolGroup(marker.toolGroup)
	expanded := stripANSI(strings.Join(rowsRendered(frontend.screenTranscriptRows(100)), "\n"))
	if !strings.Contains(expanded, "$ ls") || !strings.Contains(expanded, "a.txt") || strings.Contains(expanded, "⋯") {
		t.Fatalf("click did not expand the group: %q", expanded)
	}
	frontend.toggleToolGroup(marker.toolGroup)
	frontend.showToolOutput = true
	if expanded := stripANSI(strings.Join(rowsRendered(frontend.screenTranscriptRows(100)), "\n")); !strings.Contains(expanded, "$ ls") {
		t.Fatalf("Ctrl+O state did not expand the group: %q", expanded)
	}
}

func rowsRendered(rows []screenTranscriptRow) []string {
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, row.rendered)
	}
	return lines
}

// --- item 4：inline code ---

func assistantRows(text string, width int) []screenTranscriptRow {
	return screenRowsForBlock(screenBlock{kind: screenAssistant, text: text}, width)
}

func withoutColors(t *testing.T) {
	t.Helper()
	savedCyan, savedGreen, savedMuted, savedReset := cyan, green, muted, reset
	cyan, green, muted, reset = "", "", "", ""
	t.Cleanup(func() { cyan, green, muted, reset = savedCyan, savedGreen, savedMuted, savedReset })
}

// T-UX-11
func TestInlineCodeUsesStyleInsteadOfBracketGlyphs(t *testing.T) {
	rows := assistantRows("run `go test -race ./...` now", 100)
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	rendered := rows[0].rendered
	for _, glyph := range []string{"⟦", "⟧", "`"} {
		if strings.Contains(rendered, glyph) {
			t.Fatalf("rendered row contains %q: %q", glyph, rendered)
		}
	}
	if !strings.Contains(rendered, cyan+"go test -race ./..."+green) {
		t.Fatalf("code span is not styled on its own: %q", rendered)
	}
	if got := stripANSI(rendered); !strings.Contains(got, "assistant › run go test -race ./... now") {
		t.Fatalf("plain row = %q", got)
	}
}

// T-UX-12
func TestInlineCodeFallsBackToBackticksWithoutColor(t *testing.T) {
	withoutColors(t)
	rows := assistantRows("run `go test` now", 100)
	if got := strings.TrimRight(rows[0].rendered, " "); got != "assistant › run `go test` now" {
		t.Fatalf("no-color row = %q", got)
	}
	if displayWidth(rows[0].rendered) != 100 {
		t.Fatalf("no-color row width = %d, want 100 (backticks must count toward layout)", displayWidth(rows[0].rendered))
	}
}

// T-UX-13
func TestInlineCodeCopyTextHasNoStylingGlyphs(t *testing.T) {
	rows := assistantRows("run `go test` now", 100)
	if rows[0].copyText != "run go test now" {
		t.Fatalf("copyText = %q, want original text without styling glyphs", rows[0].copyText)
	}
	frontend := &ui{screenMode: true}
	frontend.screenAdd(screenAssistant, "run `go test` now")
	rowIndex := -1
	for index, row := range frontend.screenTranscriptRows(100) {
		if row.copyable {
			rowIndex = index
		}
	}
	frontend.selection = screenSelection{active: true, start: screenPoint{row: rowIndex, col: 0}, end: screenPoint{row: rowIndex, col: 99}}
	if got := frontend.selectedTranscriptText(100); got != "run go test now" {
		t.Fatalf("selected text = %q", got)
	}
	for _, glyph := range []string{"⟦", "⟧", "", ""} {
		if strings.Contains(rows[0].copyText, glyph) {
			t.Fatalf("copyText leaks %q", glyph)
		}
	}
}

// T-UX-14：放得下的 span 不跨行；放不下才拆，且每一行的顏色獨立平衡。
func TestInlineCodeSpanWrapsAsAUnitWhenItFits(t *testing.T) {
	// prefix `assistant › ` 佔 12 欄；可用寬度 10。
	rows := assistantRows("abcdef `go test` z", 22)
	var texts []string
	for _, row := range rows {
		texts = append(texts, row.copyText)
	}
	if len(texts) < 2 || texts[0] != "abcdef " || !strings.HasPrefix(texts[1], "go test") {
		t.Fatalf("rows = %q, want the code span moved whole to the second row", texts)
	}
}

func TestInlineCodeSpanTooLongIsSplitWithBalancedStyle(t *testing.T) {
	rows := assistantRows("`abcdefghijklmnopqrstuvwxyz0123456789`", 22)
	if len(rows) < 3 {
		t.Fatalf("rows = %d, want the long span split over several rows", len(rows))
	}
	var joined strings.Builder
	for _, row := range rows {
		if !strings.Contains(row.rendered, cyan) {
			t.Fatalf("row lost the code style: %q", row.rendered)
		}
		joined.WriteString(row.copyText)
	}
	if joined.String() != "abcdefghijklmnopqrstuvwxyz0123456789" {
		t.Fatalf("copy text = %q", joined.String())
	}
}

func TestInlineCodeMarkersCannotBeForgedByContent(t *testing.T) {
	if got := sanitizeText("abc"); got != "abc" {
		t.Fatalf("sanitizeText = %q, want markers stripped", got)
	}
}

// --- item 5 / 6：turn 底部與 card 框線 ---

func completedTurnFrontend(t *testing.T, backendCaps Capabilities) *ui {
	t.Helper()
	frontend := &ui{screenMode: true, cwd: "/tmp/project", backend: acpLikeBackend("claude", backendCaps)}
	frontend.beginScreenTurn("turn-1")
	frontend.screenAdd(screenUser, "question")
	frontend.busy = true
	frontend.handleEvent(EvToolStarted{ToolID: "a", Kind: ToolCommand, Title: "$ ls"})
	frontend.handleEvent(EvToolStarted{ToolID: "b", Kind: ToolEdit, Title: "✎ Write x"})
	frontend.handleEvent(EvAssistantDelta{Text: "answer"})
	frontend.handleEvent(EvTurnCompleted{TurnID: "turn-1", Status: TurnCompleted, Label: "completed"})
	return frontend
}

func plainTranscript(frontend *ui, width int) string {
	return stripANSI(strings.Join(rowsRendered(frontend.screenTranscriptRows(width)), "\n"))
}

// T-UX-15
func TestTurnEndsWithOneRecapLine(t *testing.T) {
	frontend := completedTurnFrontend(t, Capabilities{})
	plain := plainTranscript(frontend, 100)
	if !strings.Contains(plain, "✓ completed · 2 tools · 1 file change") {
		t.Fatalf("recap line missing: %q", plain)
	}
	for _, redundant := range []string{"turn completed", "recap", "info"} {
		if strings.Contains(plain, redundant) {
			t.Fatalf("turn bottom still has %q: %q", redundant, plain)
		}
	}
	count := 0
	for _, row := range frontend.screenTranscriptRows(100) {
		if strings.Contains(stripANSI(row.rendered), "completed") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("rows mentioning completed = %d, want 1", count)
	}
}

// T-UX-16
func TestFailedTurnRecapNamesTheStatus(t *testing.T) {
	frontend := &ui{screenMode: true, backend: &fakeBackend{}, busy: true}
	frontend.beginScreenTurn("turn-1")
	frontend.screenAdd(screenUser, "question")
	frontend.handleEvent(EvTurnCompleted{TurnID: "turn-1", Status: TurnFailed, Label: "failed"})
	if plain := plainTranscript(frontend, 100); !strings.Contains(plain, "✗ failed") || strings.Contains(plain, "0 tools") {
		t.Fatalf("failed recap = %q", plain)
	}
}

// T-UX-17
func TestHoverDoesNotReverseCardChrome(t *testing.T) {
	frontend := completedTurnFrontend(t, Capabilities{ForkAtTurn: true})
	rows := frontend.screenTranscriptRows(100)
	headerIndex, actionIndex := -1, -1
	for index, row := range rows {
		if strings.HasPrefix(stripANSI(row.rendered), "── turn") {
			headerIndex = index
		}
		if len(row.actions) > 0 {
			actionIndex = index
		}
	}
	if headerIndex < 0 || actionIndex < 0 {
		t.Fatalf("header %d / action %d row missing: %q", headerIndex, actionIndex, plainTranscript(frontend, 100))
	}
	for _, index := range []int{headerIndex, actionIndex} {
		frontend.hoverText, frontend.hoverRow = rows[index].tooltip, index
		rendered := frontend.screenRenderedRow(rows[index], index, 100)
		if index == headerIndex && strings.Contains(rendered, "\x1b[7m") {
			t.Fatalf("hover reversed the header rule: %q", rendered)
		}
		if index == actionIndex {
			if strings.Count(rendered, "\x1b[7m") != len(rows[index].actions) {
				t.Fatalf("hover reversed %d spans on the action row, want one per action: %q", strings.Count(rendered, "\x1b[7m"), rendered)
			}
			if strings.Contains(rendered, "\x1b[7m ") {
				t.Fatalf("hover reversed blank space on the action row: %q", rendered)
			}
		}
	}
}

func TestHoverOnToolMarkerReversesOnlyItsText(t *testing.T) {
	frontend := &ui{screenMode: true, backend: &fakeBackend{}}
	frontend.beginScreenTurn("t1")
	frontend.screenAdd(screenUser, "q")
	addToolUse(frontend, "a", "$ ls", "", "")
	rows := frontend.screenTranscriptRows(100)
	for index, row := range rows {
		if row.toolGroup == "" {
			continue
		}
		frontend.hoverText, frontend.hoverRow = row.tooltip, index
		rendered := frontend.screenRenderedRow(row, index, 100)
		if !strings.Contains(rendered, "\x1b[7m⋯ 1 tool\x1b[27m") {
			t.Fatalf("marker hover = %q", rendered)
		}
	}
}

// T-UX-18
func TestSelectedTurnShowsOnlyTheComposerAsAskAffordance(t *testing.T) {
	frontend := completedTurnFrontend(t, Capabilities{ForkAtTurn: true})
	frontend.selectScreenTurnForAsk(screenTurnTarget{key: frontend.transcript.entries[0].turnKey, id: "turn-1", ordinal: 1})

	plain := plainTranscript(frontend, 100)
	if strings.Contains(plain, "[ask]") {
		t.Fatalf("selected turn still shows [ask] in the card: %q", plain)
	}
	if !strings.Contains(plain, "[fork]") || !strings.Contains(plain, "── turn 1 · selected") {
		t.Fatalf("selected turn lost [fork] or the selected mark: %q", plain)
	}
	composer := stripANSI(strings.Join(frontend.screenComposerLines(100), "\n"))
	if !strings.Contains(composer, "ask turn 1") {
		t.Fatalf("composer does not name the target: %q", composer)
	}
}

// T-UX-19
func TestSelectedTurnHintIsNotRepeated(t *testing.T) {
	frontend := completedTurnFrontend(t, Capabilities{})
	frontend.thread.Model = "opus"
	frontend.selectScreenTurnForAsk(screenTurnTarget{key: frontend.transcript.entries[0].turnKey, id: "turn-1", ordinal: 1})

	label := frontend.screenStatusLabel()
	if strings.Contains(label, "ask about turn") || strings.Contains(label, "type follow-up") {
		t.Fatalf("status label repeats the composer hint: %q", label)
	}
	dock := stripANSI(frontend.screenDockLine(200))
	if strings.Count(dock, "turn 1") > 1 || strings.Contains(dock, "type follow-up") {
		t.Fatalf("dock repeats the selected turn: %q", dock)
	}
	for _, row := range frontend.screenTranscriptRows(100) {
		if row.turnKey != "" && strings.Contains(row.tooltip, "click to ask") {
			t.Fatalf("selected turn tooltip still invites a click to ask: %q", row.tooltip)
		}
	}
}

// T-UX-20
func TestNoActionRowWhenNothingToShow(t *testing.T) {
	frontend := completedTurnFrontend(t, Capabilities{})
	frontend.selectScreenTurnForAsk(screenTurnTarget{key: frontend.transcript.entries[0].turnKey, id: "turn-1", ordinal: 1})
	for _, row := range frontend.screenTranscriptRows(100) {
		if len(row.actions) > 0 {
			t.Fatalf("unexpected action row: %q", row.rendered)
		}
	}
}

// T-UX-21
func TestTurnCardIsAHeaderRuleWithoutFrame(t *testing.T) {
	frontend := completedTurnFrontend(t, Capabilities{ForkAtTurn: true})
	rows := frontend.screenTranscriptRows(80)
	if first := stripANSI(rows[0].rendered); !strings.HasPrefix(first, "── turn 1 ─") || displayWidth(first) != 80 {
		t.Fatalf("header row = %q", first)
	}
	for _, row := range rows {
		plain := stripANSI(row.rendered)
		for _, frame := range []string{"┌", "┐", "└", "┘", "│"} {
			if strings.Contains(plain, frame) {
				t.Fatalf("turn card row has frame glyph %q: %q", frame, plain)
			}
		}
		if displayWidth(plain) != 80 {
			t.Fatalf("row width = %d, want 80: %q", displayWidth(plain), plain)
		}
	}
	for _, action := range func() []screenActionRange {
		for _, row := range rows {
			if len(row.actions) > 0 {
				return row.actions
			}
		}
		return nil
	}() {
		if action.start < 2 {
			t.Fatalf("action range = %#v", action)
		}
	}
}
