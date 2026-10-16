package lumen

import (
	"strings"
	"testing"
)

func collectKeys(input string) []keyEvent {
	keys := make(chan keyEvent, 64)
	go readKeys(strings.NewReader(input), keys)
	var got []keyEvent
	for key := range keys {
		got = append(got, key)
	}
	return got
}

func TestReadKeysIgnoresUnknownCSI(t *testing.T) {
	for _, input := range []string{"\x1b[?1;2c", "\x1b[999;9u", "\x1b[I", "\x1b[O", "\x1b[118;5:1u"} {
		got := collectKeys(input)
		for _, key := range got {
			if key.typ == keyEscape {
				t.Fatalf("%q emitted keyEscape", input)
			}
		}
	}
	if got := collectKeys("\x1b[999;9ua"); len(got) != 1 || got[0].typ != keyRune || got[0].rune != 'a' {
		t.Fatalf("unknown CSI-u must emit nothing: %#v", got)
	}
}

func TestReadKeysKeepsBareEscape(t *testing.T) {
	if got := collectKeys("\x1b"); len(got) != 1 || got[0].typ != keyEscape {
		t.Fatalf("bare ESC = %#v", got)
	}
	if got := collectKeys("\x1bx"); len(got) != 1 || got[0].typ != keyEscape {
		t.Fatalf("ESC x = %#v", got)
	}
}

func TestReadKeysParsesKittyCSIu(t *testing.T) {
	cases := []struct {
		input string
		want  keyType
	}{
		{"\x1b[27u", keyEscape},
		{"\x1b[27;1u", keyEscape},
		{"\x1b[99;5u", keyCtrlC},
		{"\x1b[100;5u", keyCtrlD},
		{"\x1b[120;5u", keyCtrlX},
		{"\x1b[111;5u", keyCtrlO},
		{"\x1b[117;5u", keyCtrlU},
		{"\x1b[92;5u", keyCtrlBackslash},
		{"\x1b[118;5u", keyCtrlV},
		{"\x1b[99;5:1u", keyCtrlC},
		{"\x1b[99:67;5u", keyCtrlC},
		{"\x1b[13u", keyEnter},
		{"\x1b[9u", keyTab},
		{"\x1b[127u", keyBackspace},
		{"\x1b[13;5u", keyCtrlEnter},
		{"\x16", keyCtrlV},
	}
	for _, tc := range cases {
		got := collectKeys(tc.input)
		if len(got) != 1 || got[0].typ != tc.want {
			t.Errorf("%q = %#v, want %v", tc.input, got, tc.want)
		}
	}
	// key release event 不重複觸發
	if got := collectKeys("\x1b[99;5:3u"); len(got) != 0 {
		t.Errorf("release event = %#v, want none", got)
	}
}

func TestReadKeysParsesBracketedPaste(t *testing.T) {
	got := collectKeys("\x1b[200~a\r\nb\rc\x01\td\x1b[31m\x1b[201~z")
	if len(got) != 2 || got[0].typ != keyPaste || got[1].typ != keyRune {
		t.Fatalf("paste events = %#v", got)
	}
	if want := "a\nb\nc    d[31m"; got[0].text != want {
		t.Fatalf("paste text = %q, want %q", got[0].text, want)
	}
}

func TestPasteInsertsWithoutSubmitting(t *testing.T) {
	frontend := &ui{screenMode: true}
	frontend.input = []rune("ab")
	frontend.cursor = 1
	if err := frontend.handleKey(keyEvent{typ: keyPaste, text: "x\ny\n"}); err != nil {
		t.Fatal(err)
	}
	if got := string(frontend.input); got != "ax\ny\nb" {
		t.Fatalf("input = %q", got)
	}
	if frontend.cursor != 5 {
		t.Fatalf("cursor = %d", frontend.cursor)
	}
	frontend.busy = true
	if err := frontend.handleKey(keyEvent{typ: keyPaste, text: "z"}); err != nil {
		t.Fatal(err)
	}
	if got := string(frontend.input); got != "ax\ny\nzb" {
		t.Fatalf("busy input = %q", got)
	}
}

func TestCtrlVAndControlRunesDoNotEditComposer(t *testing.T) {
	// Ctrl+V 會讀剪貼簿：注入空剪貼簿，避免測試依賴本機狀態。
	frontend := &ui{screenMode: true, readClipboard: func(string) (clipboardContent, error) { return clipboardContent{}, nil }}
	t.Cleanup(frontend.releaseImages)
	for _, key := range []keyEvent{{typ: keyCtrlV}, {typ: keyRune, rune: 0x16}, {typ: keyRune, rune: 0x01}} {
		if err := frontend.handleKey(key); err != nil {
			t.Fatalf("%#v error = %v", key, err)
		}
	}
	if len(frontend.input) != 0 {
		t.Fatalf("input = %q", string(frontend.input))
	}
}

func TestKittyCtrlCGoesThroughConfirmation(t *testing.T) {
	keys := collectKeys("\x1b[99;5u")
	frontend := &ui{screenMode: true}
	if err := frontend.handleKey(keys[0]); err != nil || frontend.lastCtrlC.IsZero() {
		t.Fatalf("Ctrl+C err=%v lastCtrlC=%v", err, frontend.lastCtrlC)
	}
}

func TestFullscreenLifecycleEnablesBracketedPaste(t *testing.T) {
	var out strings.Builder
	l := newFullscreenTerminalLifecycle(0, &out)
	if !strings.Contains(l.enter, "\x1b[?2004h") || !strings.Contains(l.exit, "\x1b[?2004l") {
		t.Fatalf("lifecycle enter=%q exit=%q", l.enter, l.exit)
	}
}
