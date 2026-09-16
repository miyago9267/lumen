package lumen

import (
	"strconv"
	"strings"
)

type sessionEntry struct {
	id           string
	order        uint64
	role         screenBlockKind
	kind         screenBlockKind
	turnKey      string
	turnID       string
	blockID      string
	parentID     string
	toolBoundary uint64
	text         string
	lifecycle    string
	toolActivity bool
	toolGroup    string
}

type canonicalTranscript struct {
	entries   []sessionEntry
	nextID    uint64
	nextOrder uint64
}

func (t *canonicalTranscript) append(entry sessionEntry) sessionEntry {
	t.nextID++
	if entry.id == "" {
		entry.id = formatSessionEntryID(t.nextID)
	}
	if entry.blockID == "" {
		entry.blockID = entry.id
	}
	t.nextOrder++
	entry.order = t.nextOrder
	if entry.role == screenEvent && entry.kind != screenEvent {
		entry.role = entry.kind
	}
	if entry.kind == screenEvent && entry.role != screenEvent {
		entry.kind = entry.role
	}
	t.entries = append(t.entries, entry)
	return entry
}

func (t *canonicalTranscript) clear() {
	t.entries = nil
	t.nextID = 0
	t.nextOrder = 0
}

func (t *canonicalTranscript) resequence() {
	for index := range t.entries {
		t.entries[index].order = uint64(index + 1)
	}
	t.nextOrder = uint64(len(t.entries))
}

func (t *canonicalTranscript) indexByID(id string) int {
	if id == "" {
		return -1
	}
	for index := range t.entries {
		if t.entries[index].id == id {
			return index
		}
	}
	return -1
}

func (t *canonicalTranscript) blocks(limit int) []screenBlock {
	if limit < 1 || len(t.entries) <= limit {
		limit = len(t.entries)
	}
	start := len(t.entries) - limit
	if start < 0 {
		start = 0
	}
	blocks := make([]screenBlock, 0, len(t.entries)-start)
	for _, entry := range t.entries[start:] {
		blocks = append(blocks, screenBlock{
			kind:         entry.kind,
			text:         entry.text,
			turnKey:      entry.turnKey,
			turnID:       entry.turnID,
			toolActivity: entry.toolActivity,
			toolGroup:    entry.toolGroup,
		})
	}
	return blocks
}

func (u *ui) syncScreenProjection() {
	u.screenBlocks = u.transcript.blocks(maxScreenBlocks)
}

func (u *ui) projectedScreenBlocks() []screenBlock {
	if len(u.transcript.entries) == 0 {
		return u.screenBlocks
	}
	return u.transcript.blocks(maxScreenBlocks)
}

func (u *ui) screenReaderProjection() []string {
	blocks := u.projectedScreenBlocks()
	lines := make([]string, 0, len(blocks))
	for _, block := range blocks {
		text := strings.Join(strings.Fields(sanitizeText(block.text)), " ")
		if text == "" {
			continue
		}
		lines = append(lines, screenBlockLabel(block)+": "+text)
	}
	return lines
}

func (u *ui) clearScreenTranscript() {
	u.transcript.clear()
	u.syncScreenProjection()
}

func formatSessionEntryID(sequence uint64) string {
	return "session-entry-" + strconv.FormatUint(sequence, 10)
}
