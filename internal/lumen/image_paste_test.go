package lumen

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// writeTestPNG 在 dir 寫一張 width x height 的 PNG，回傳路徑。
func writeTestPNG(t *testing.T, dir string, width, height int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 200, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// imageUI 建出 screen mode 的 ui，剪貼簿讀取注入為 content / err。
func imageUI(t *testing.T, backend Backend, content clipboardContent, err error) *ui {
	t.Helper()
	frontend := &ui{
		backend:    backend,
		thread:     threadSummary{ID: "thread-1"},
		screenMode: true,
		out:        &bytes.Buffer{},
	}
	frontend.readClipboard = func(string) (clipboardContent, error) { return content, err }
	t.Cleanup(frontend.releaseImages)
	return frontend
}

func imageBackend() *fakeBackend {
	return &fakeBackend{caps: Capabilities{ImagePrompt: true}}
}

func pasteImage(t *testing.T, frontend *ui) {
	t.Helper()
	if err := frontend.handleKey(keyEvent{typ: keyCtrlV}); err != nil {
		t.Fatalf("Ctrl+V error = %v", err)
	}
}

// T-IMG-01
func TestCtrlVInsertsChipAndRecordsAttachment(t *testing.T) {
	path := writeTestPNG(t, t.TempDir(), 40, 20)
	frontend := imageUI(t, imageBackend(), clipboardContent{ImagePath: path}, nil)
	frontend.input = []rune("ab")
	frontend.cursor = 1

	pasteImage(t, frontend)
	if got := string(frontend.input); got != "a[Image #1]b" || frontend.cursor != 1+len("[Image #1]") {
		t.Fatalf("input = %q cursor = %d", got, frontend.cursor)
	}
	attachment := frontend.images[1]
	if attachment == nil || attachment.Path != path || attachment.MIME != "image/png" || attachment.Width != 40 || attachment.Height != 20 || attachment.Size <= 0 {
		t.Fatalf("attachment = %#v", attachment)
	}
	pasteImage(t, frontend)
	if got := string(frontend.input); got != "a[Image #1][Image #2]b" {
		t.Fatalf("second paste input = %q", got)
	}
}

// T-IMG-02
func TestCtrlVFallsBackToClipboardText(t *testing.T) {
	frontend := imageUI(t, imageBackend(), clipboardContent{Text: "hello\r\nworld"}, nil)
	frontend.input = []rune("ab")
	frontend.cursor = 1
	pasteImage(t, frontend)
	if got := string(frontend.input); got != "ahello\nworldb" {
		t.Fatalf("input = %q", got)
	}
	if len(frontend.images) != 0 {
		t.Fatalf("images = %#v", frontend.images)
	}
}

// T-IMG-03 / T-IMG-05
func TestCtrlVNoticesWhenNothingToPaste(t *testing.T) {
	frontend := imageUI(t, imageBackend(), clipboardContent{}, nil)
	frontend.input = []rune("x")
	frontend.cursor = 1
	pasteImage(t, frontend)
	if string(frontend.input) != "x" || !strings.Contains(stripANSI(frontend.screenFrame(100, 24)), "clipboard has no image") {
		t.Fatalf("empty clipboard: input=%q frame=%q", string(frontend.input), stripANSI(frontend.screenFrame(100, 24)))
	}

	failing := imageUI(t, imageBackend(), clipboardContent{}, errors.New("no clipboard command found"))
	pasteImage(t, failing)
	if len(failing.input) != 0 || !strings.Contains(stripANSI(failing.screenFrame(100, 24)), "clipboard unavailable: no clipboard command found") {
		t.Fatalf("failing clipboard: input=%q frame=%q", string(failing.input), stripANSI(failing.screenFrame(100, 24)))
	}
}

// T-IMG-04
func TestCtrlVRejectsImageWhenBackendCannotTakeImages(t *testing.T) {
	temp := writeTestPNG(t, t.TempDir(), 8, 8)
	frontend := imageUI(t, &fakeBackend{}, clipboardContent{ImagePath: temp, Temp: true}, nil)
	pasteImage(t, frontend)
	if len(frontend.input) != 0 || len(frontend.images) != 0 {
		t.Fatalf("input = %q images = %#v", string(frontend.input), frontend.images)
	}
	if !strings.Contains(stripANSI(frontend.screenFrame(100, 24)), "fake does not accept images") {
		t.Fatalf("notice missing: %q", stripANSI(frontend.screenFrame(100, 24)))
	}
	if _, err := os.Stat(temp); !os.IsNotExist(err) {
		t.Fatalf("temp file should be removed, stat err = %v", err)
	}
}

// T-IMG-06
func TestClipboardImageHelpers(t *testing.T) {
	for path, want := range map[string]string{
		"/a/b.PNG": "image/png", "/a/b.jpg": "image/jpeg", "/a/b.jpeg": "image/jpeg",
		"/a/b.gif": "image/gif", "/a/b.webp": "image/webp", "/a/b.txt": "", "/a/b": "",
	} {
		if got := imageMIMEByExtension(path); got != want {
			t.Errorf("imageMIMEByExtension(%q) = %q, want %q", path, got, want)
		}
	}
	dir := t.TempDir()
	pngPath := writeTestPNG(t, dir, 4, 4)
	renamed := filepath.Join(dir, "looks-like.jpg")
	if err := os.Rename(pngPath, renamed); err != nil {
		t.Fatal(err)
	}
	if mime, err := detectImageMIME(renamed); err != nil || mime != "image/png" {
		t.Fatalf("detectImageMIME(sniff) = %q, %v", mime, err)
	}
	text := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(text, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := detectImageMIME(text); err == nil {
		t.Fatal("text file accepted as image")
	}
}

// T-IMG-07
func TestBackspaceRemovesWholeChip(t *testing.T) {
	path := writeTestPNG(t, t.TempDir(), 4, 4)
	frontend := imageUI(t, imageBackend(), clipboardContent{ImagePath: path}, nil)
	pasteImage(t, frontend)
	frontend.insertText(" tail")
	frontend.cursor = len("[Image #1]") // chip 剛好後面
	if err := frontend.handleKey(keyEvent{typ: keyBackspace}); err != nil {
		t.Fatal(err)
	}
	if got := string(frontend.input); got != " tail" || frontend.cursor != 0 {
		t.Fatalf("after backspace right after chip: %q cursor=%d", got, frontend.cursor)
	}

	pasteImage(t, frontend) // #2 at cursor 0
	frontend.cursor = 4     // chip 中間
	if err := frontend.handleKey(keyEvent{typ: keyBackspace}); err != nil {
		t.Fatal(err)
	}
	if got := string(frontend.input); got != " tail" {
		t.Fatalf("after backspace inside chip: %q", got)
	}

	literal := imageUI(t, imageBackend(), clipboardContent{}, nil)
	literal.input = []rune("[Image #9]")
	literal.cursor = len(literal.input)
	if err := literal.handleKey(keyEvent{typ: keyBackspace}); err != nil {
		t.Fatal(err)
	}
	if got := string(literal.input); got != "[Image #9" {
		t.Fatalf("literal text without attachment must delete one rune, got %q", got)
	}
}

// T-IMG-08
func TestDeleteRemovesWholeChipOrOneRune(t *testing.T) {
	path := writeTestPNG(t, t.TempDir(), 4, 4)
	frontend := imageUI(t, imageBackend(), clipboardContent{ImagePath: path}, nil)
	frontend.input = []rune("xy")
	frontend.cursor = 1
	pasteImage(t, frontend) // x[Image #1]y
	frontend.cursor = 1
	if err := frontend.handleKey(keyEvent{typ: keyDelete}); err != nil {
		t.Fatal(err)
	}
	if got := string(frontend.input); got != "xy" || frontend.cursor != 1 {
		t.Fatalf("delete at chip start: %q cursor=%d", got, frontend.cursor)
	}
	if err := frontend.handleKey(keyEvent{typ: keyDelete}); err != nil {
		t.Fatal(err)
	}
	if got := string(frontend.input); got != "x" {
		t.Fatalf("delete plain rune: %q", got)
	}
	if err := frontend.handleKey(keyEvent{typ: keyDelete}); err != nil || string(frontend.input) != "x" {
		t.Fatalf("delete at end: %q, %v", string(frontend.input), err)
	}
}

func TestReadKeysParsesDelete(t *testing.T) {
	if got := collectKeys("\x1b[3~"); len(got) != 1 || got[0].typ != keyDelete {
		t.Fatalf("Delete key = %#v", got)
	}
}

// T-IMG-09 / T-IMG-16 / T-IMG-24
func TestSubmitCollectsOnlyPresentChips(t *testing.T) {
	dir := t.TempDir()
	first := writeTestPNG(t, dir, 4, 4)
	backend := imageBackend()
	frontend := imageUI(t, backend, clipboardContent{ImagePath: first}, nil)
	pasteImage(t, frontend) // #1
	pasteImage(t, frontend) // #2
	pasteImage(t, frontend) // #3
	// 刪掉 #2：游標移到 #2 後面再 Backspace。
	frontend.cursor = len("[Image #1][Image #2]")
	if err := frontend.handleKey(keyEvent{typ: keyBackspace}); err != nil {
		t.Fatal(err)
	}
	frontend.insertText(" look [Image #1] again")

	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatalf("submit error = %v", err)
	}
	// 刪除 #2 後游標停在 #1 後面，所以後來插入的文字接在 #1 與 #3 之間。
	if len(backend.prompts) != 1 || backend.prompts[0] != "[Image #1] look [Image #1] again[Image #3]" {
		t.Fatalf("prompts = %q", backend.prompts)
	}
	images := backend.images[0]
	if len(images) != 2 || images[0].Path != first || images[0].MIME != "image/png" {
		t.Fatalf("images = %#v", images)
	}
	if plain := stripANSI(frontend.screenFrame(100, 24)); !strings.Contains(plain, "[Image #1] look [Image #1] again[Image #3]") {
		t.Fatalf("user block should keep chips: %q", plain)
	}
}

// T-IMG-10
func TestQueuedPromptKeepsItsImages(t *testing.T) {
	path := writeTestPNG(t, t.TempDir(), 4, 4)
	backend := imageBackend()
	frontend := imageUI(t, backend, clipboardContent{ImagePath: path}, nil)
	frontend.busy = true
	pasteImage(t, frontend)
	frontend.insertText(" queued")
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err != nil {
		t.Fatal(err)
	}
	if len(frontend.queuedPrompts) != 1 {
		t.Fatalf("queued = %#v", frontend.queuedPrompts)
	}
	frontend.busy = false
	frontend.startNextPrompt()
	if len(backend.images) != 1 || len(backend.images[0]) != 1 || backend.images[0][0].Path != path {
		t.Fatalf("queued prompt images = %#v", backend.images)
	}
}

// T-IMG-15
func TestSubmitWithChipRejectedWhenBackendCannotTakeImages(t *testing.T) {
	path := writeTestPNG(t, t.TempDir(), 4, 4)
	backend := imageBackend()
	frontend := imageUI(t, backend, clipboardContent{ImagePath: path}, nil)
	pasteImage(t, frontend)
	frontend.insertText(" hi")
	before := string(frontend.input)
	backend.caps = Capabilities{} // 例如切換到不支援圖片的 backend

	err := frontend.handleKey(keyEvent{typ: keyEnter})
	if err == nil || !strings.Contains(err.Error(), "does not accept images") {
		t.Fatalf("submit error = %v", err)
	}
	if len(backend.prompts) != 0 || string(frontend.input) != before {
		t.Fatalf("prompt sent or input lost: prompts=%q input=%q", backend.prompts, string(frontend.input))
	}

	frontend.busy = true
	if err := frontend.handleKey(keyEvent{typ: keyEnter}); err == nil || len(frontend.queuedPrompts) != 0 {
		t.Fatalf("queue must reject unsupported images: err=%v queued=%#v", err, frontend.queuedPrompts)
	}
}

// T-IMG-13 / T-IMG-12
func TestACPImageCapabilityFromInitialize(t *testing.T) {
	with := startFakeACP(t, map[string]string{"FAKEACP_CAPS": "load,list,image"}, nil)
	if !with.backend.Caps().ImagePrompt {
		t.Fatal("ImagePrompt should be true when promptCapabilities.image is true")
	}
}

func TestACPImageCapabilityAbsent(t *testing.T) {
	without := startFakeACP(t, nil, nil)
	if without.backend.Caps().ImagePrompt {
		t.Fatal("ImagePrompt should be false when promptCapabilities.image is false")
	}
}

// T-IMG-11
func TestACPPromptSendsImageBlocksAfterText(t *testing.T) {
	run := startFakeACP(t, map[string]string{"FAKEACP_CAPS": "load,list,image"}, nil)
	sid := newFakeSession(t, run)
	path := writeTestPNG(t, t.TempDir(), 6, 6)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.backend.Prompt(sid, "happy", []promptImage{{Path: path, MIME: "image/png"}}, runtimeOverrides{}); err != nil {
		t.Fatalf("Prompt error = %v", err)
	}
	eventsUntil(t, run.events, isTurnCompleted)

	params := fakeACPRequests(t, run.logPath, "session/prompt")
	if len(params) != 1 {
		t.Fatalf("session/prompt requests = %d", len(params))
	}
	var got struct {
		Prompt []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			MimeType string `json:"mimeType"`
			Data     string `json:"data"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(params[0], &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Prompt) != 2 || got.Prompt[0].Type != "text" || got.Prompt[0].Text != "happy" {
		t.Fatalf("prompt = %s", params[0])
	}
	image := got.Prompt[1]
	if image.Type != "image" || image.MimeType != "image/png" || image.Data != base64.StdEncoding.EncodeToString(raw) {
		t.Fatalf("image block = %+v", image)
	}
}

// T-IMG-12
func TestACPPromptWithImagesFailsWithoutCapability(t *testing.T) {
	run := startFakeACP(t, nil, nil)
	sid := newFakeSession(t, run)
	path := writeTestPNG(t, t.TempDir(), 6, 6)
	_, err := run.backend.Prompt(sid, "happy", []promptImage{{Path: path, MIME: "image/png"}}, runtimeOverrides{})
	if err == nil || !strings.Contains(err.Error(), "does not accept images") {
		t.Fatalf("Prompt error = %v", err)
	}
	if got := fakeACPRequests(t, run.logPath, "session/prompt"); len(got) != 0 {
		t.Fatalf("session/prompt sent despite missing capability: %s", got)
	}
	// turn 沒有被佔用：之後的純文字 prompt 仍可送出。
	run.runTurn(t, sid, "happy")
}

// T-IMG-14
func TestCodexPromptSendsLocalImageItems(t *testing.T) {
	reader, writer := io.Pipe()
	recorder := &paramsRecorder{output: writer}
	client := newAppServer(recorder, reader, &exec.Cmd{})
	go client.readLoop()
	backend := newCodexBackend(client, "thread-1")
	t.Cleanup(backend.Close)
	if !backend.Caps().ImagePrompt {
		t.Fatal("codex ImagePrompt should be true")
	}
	images := []promptImage{{Path: "/tmp/a.png", MIME: "image/png"}, {Path: "/tmp/b.jpg", MIME: "image/jpeg"}}
	if _, err := backend.Prompt("thread-1", "look", images, runtimeOverrides{}); err != nil {
		t.Fatalf("Prompt error = %v", err)
	}
	var got struct {
		ThreadID string           `json:"threadId"`
		Input    []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(recorder.params("turn/start"), &got); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"type": "text", "text": "look"},
		{"type": "localImage", "path": "/tmp/a.png"},
		{"type": "localImage", "path": "/tmp/b.jpg"},
	}
	encodedGot, _ := json.Marshal(got.Input)
	encodedWant, _ := json.Marshal(want)
	if string(encodedGot) != string(encodedWant) {
		t.Fatalf("input = %s, want %s", encodedGot, encodedWant)
	}
}

// paramsRecorder 記錄 request params 並回覆固定的 turn/start 結果。
type paramsRecorder struct {
	output *io.PipeWriter
	mu     sync.Mutex
	seen   map[string]json.RawMessage
}

func (r *paramsRecorder) Write(value []byte) (int, error) {
	var request struct {
		ID     *int64          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(value), &request); err != nil {
		return 0, err
	}
	r.mu.Lock()
	if r.seen == nil {
		r.seen = map[string]json.RawMessage{}
	}
	r.seen[request.Method] = request.Params
	r.mu.Unlock()
	if request.ID == nil {
		return len(value), nil
	}
	encoded, _ := json.Marshal(map[string]any{"id": *request.ID, "result": map[string]any{"turn": map[string]any{"id": "turn-1"}}})
	if _, err := r.output.Write(append(encoded, '\n')); err != nil {
		return 0, err
	}
	return len(value), nil
}

func (r *paramsRecorder) Close() error { return r.output.Close() }

func (r *paramsRecorder) params(method string) json.RawMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen[method]
}

// ---- preview ----

func chipUI(t *testing.T, kitty bool) (*ui, *imageAttachment) {
	t.Helper()
	path := writeTestPNG(t, t.TempDir(), 80, 40)
	frontend := imageUI(t, imageBackend(), clipboardContent{ImagePath: path}, nil)
	frontend.kittyGraphics = kitty
	pasteImage(t, frontend)
	return frontend, frontend.images[1]
}

// T-IMG-17
func TestFocusedChipRule(t *testing.T) {
	frontend, _ := chipUI(t, true)
	frontend.insertText(" tail")
	chipLen := len("[Image #1]")
	cases := []struct {
		cursor int
		want   bool
	}{{0, true}, {1, true}, {chipLen - 1, true}, {chipLen, true}, {chipLen + 1, false}, {chipLen + 5, false}}
	for _, item := range cases {
		frontend.cursor = item.cursor
		n, ok := frontend.focusedChip()
		if ok != item.want || (ok && n != 1) {
			t.Errorf("cursor %d: focusedChip = %d, %v; want %v", item.cursor, n, ok, item.want)
		}
	}
	frontend.cursor = chipLen
	for name, mutate := range map[string]func(){
		"inline":   func() { frontend.screenMode = false },
		"approval": func() { frontend.approval = &approvalRequest{} },
		"dashboard": func() {
			frontend.dashboard = true
		},
	} {
		saved := *frontend
		mutate()
		if frontend.screenPreview(100, 24) != nil {
			t.Errorf("%s: preview should be hidden", name)
		}
		*frontend = saved
	}
	frontend.cursor = chipLen
	if frontend.screenPreview(100, 24) == nil {
		t.Fatal("preview should be visible with cursor right after chip")
	}
}

// T-IMG-18
func TestDetectKittyGraphics(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	for name, values := range map[string]map[string]string{
		"ghostty":      {"TERM_PROGRAM": "ghostty"},
		"kitty":        {"TERM_PROGRAM": "kitty"},
		"wezterm":      {"TERM_PROGRAM": "WezTerm"},
		"term kitty":   {"TERM": "xterm-kitty"},
		"term ghostty": {"TERM": "xterm-ghostty"},
	} {
		if !detectKittyGraphics(env(values)) {
			t.Errorf("%s should be detected", name)
		}
	}
	for name, values := range map[string]map[string]string{
		"tmux":    {"TERM_PROGRAM": "tmux", "TERM": "tmux-256color"},
		"apple":   {"TERM_PROGRAM": "Apple_Terminal", "TERM": "xterm-256color"},
		"unknown": {},
	} {
		if detectKittyGraphics(env(values)) {
			t.Errorf("%s should not be detected", name)
		}
	}
}

// T-IMG-19
func TestKittyEscapeGeneration(t *testing.T) {
	data := bytes.Repeat([]byte{0xAB}, 10000) // base64 約 13336 字元 -> 4 個 chunk
	seq := kittyTransmitSeq(7, data)
	chunks := strings.Split(strings.TrimSuffix(seq, "\x1b\\"), "\x1b\\")
	if len(chunks) != 4 {
		t.Fatalf("chunks = %d", len(chunks))
	}
	var payload strings.Builder
	for index, chunk := range chunks {
		if !strings.HasPrefix(chunk, "\x1b_G") {
			t.Fatalf("chunk %d: %q", index, chunk[:min(20, len(chunk))])
		}
		controls, body, _ := strings.Cut(strings.TrimPrefix(chunk, "\x1b_G"), ";")
		if len(body) > 4096 {
			t.Fatalf("chunk %d has %d bytes", index, len(body))
		}
		payload.WriteString(body)
		if index == 0 && controls != "a=t,f=100,i=7,q=2,m=1" {
			t.Fatalf("first controls = %q", controls)
		}
		if index > 0 && index < len(chunks)-1 && controls != "m=1" {
			t.Fatalf("middle controls = %q", controls)
		}
		if index == len(chunks)-1 && controls != "m=0" {
			t.Fatalf("last controls = %q", controls)
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(payload.String())
	if err != nil || !bytes.Equal(decoded, data) {
		t.Fatalf("payload round trip failed: %v", err)
	}
	if small := kittyTransmitSeq(3, []byte("x")); small != "\x1b_Ga=t,f=100,i=3,q=2,m=0;eA==\x1b\\" {
		t.Fatalf("single chunk = %q", small)
	}
	if got := kittyPlaceSeq(7, 30, 9); got != "\x1b_Ga=p,i=7,p=1,c=30,r=9,C=1,q=2\x1b\\" {
		t.Fatalf("place = %q", got)
	}
	if got := kittyDeleteSeq(7, false); got != "\x1b_Ga=d,d=i,i=7,p=1,q=2\x1b\\" {
		t.Fatalf("delete placement = %q", got)
	}
	if got := kittyDeleteSeq(7, true); got != "\x1b_Ga=d,d=I,i=7,q=2\x1b\\" {
		t.Fatalf("free image = %q", got)
	}
}

// T-IMG-20
func TestRenderScreenTransmitsOnceAndDeletesWhenHidden(t *testing.T) {
	frontend, _ := chipUI(t, true)
	out := frontend.out.(*bytes.Buffer)
	frontend.cursor = len("[Image #1]")

	frontend.renderScreen()
	frontend.renderScreen()
	frontend.renderScreen()
	rendered := out.String()
	if got := strings.Count(rendered, "a=t,f=100,i=1"); got != 1 {
		t.Fatalf("transmit count = %d, want 1", got)
	}
	if got := strings.Count(rendered, "a=p,i=1,p=1"); got != 3 {
		t.Fatalf("place count = %d, want 3", got)
	}
	if strings.Contains(rendered, "a=d") {
		t.Fatal("delete emitted while preview visible")
	}

	out.Reset()
	frontend.insertText(" x") // 游標離開 chip
	frontend.renderScreen()
	if !strings.Contains(out.String(), "a=d,d=i,i=1,p=1") || strings.Contains(out.String(), "a=p") {
		t.Fatalf("hide render = %q", out.String())
	}
	out.Reset()
	frontend.renderScreen()
	if strings.Contains(out.String(), "_G") {
		t.Fatalf("idle render emitted graphics: %q", out.String())
	}

	frontend.cursor = 0
	out.Reset()
	frontend.renderScreen()
	if strings.Contains(out.String(), "a=t,") || !strings.Contains(out.String(), "a=p,i=1") {
		t.Fatalf("re-focus must place without re-transmitting: %q", out.String())
	}

	out.Reset()
	frontend.releaseImages()
	if !strings.Contains(out.String(), "a=d,d=I,i=1") {
		t.Fatalf("exit must free image: %q", out.String())
	}
}

func TestPreviewWithoutKittyOnlyShowsCaption(t *testing.T) {
	frontend, _ := chipUI(t, false)
	out := frontend.out.(*bytes.Buffer)
	frontend.renderScreen()
	if strings.Contains(out.String(), "_G") {
		t.Fatalf("graphics emitted on non-kitty terminal: %q", out.String())
	}
	if !strings.Contains(stripANSI(out.String()), "Image #1 · 80x40 · PNG") {
		t.Fatalf("caption missing: %q", stripANSI(out.String()))
	}
}

// T-IMG-21
func TestFrameUnchangedWithoutFocusedChip(t *testing.T) {
	frontend, _ := chipUI(t, true)
	frontend.insertText(" tail")
	frontend.cursor = len(frontend.input) // 離開 chip

	baseline := *frontend
	baseline.images = nil // 沒有 attachment 就沒有 chip，等同今天的 frame
	if got, want := frontend.screenFrame(100, 24), baseline.screenFrame(100, 24); got != want {
		t.Fatalf("frame changed without focused chip:\n%q\n%q", got, want)
	}
	frontend.cursor = len("[Image #1]")
	focused := stripANSI(frontend.screenFrame(100, 24))
	rows := strings.Split(focused, "\r\n")
	if len(rows) != 24 {
		t.Fatalf("rows = %d", len(rows))
	}
	captionRow := -1
	for index, row := range rows {
		if strings.Contains(row, "Image #1 · 80x40 · PNG") {
			captionRow = index
		}
	}
	// 倒數第 N 列是 composer；caption 必須緊貼在 composer 上緣之前（gap row）。
	if captionRow < 0 || !strings.HasPrefix(rows[captionRow+1], "┌─ prompt") {
		t.Fatalf("caption not in gap row (row %d):\n%s", captionRow, focused)
	}
}

// T-IMG-22
func TestImageCaption(t *testing.T) {
	attachment := &imageAttachment{N: 2, Path: "/tmp/x.png", MIME: "image/png", Size: 230 * 1024, Width: 800, Height: 600}
	if got, want := imageCaption(attachment), "Image #2 · 800x600 · PNG · 230 KB · /tmp/x.png"; got != want {
		t.Fatalf("caption = %q, want %q", got, want)
	}
	unknown := &imageAttachment{N: 3, Path: "/tmp/y.webp", MIME: "image/webp", Size: 1536 * 1024}
	if got, want := imageCaption(unknown), "Image #3 · WEBP · 1.5 MB · /tmp/y.webp"; got != want {
		t.Fatalf("caption = %q, want %q", got, want)
	}
	if got := imageCaption(&imageAttachment{N: 1, Path: "/p", MIME: "image/gif", Size: 12, Width: 1, Height: 1}); got != "Image #1 · 1x1 · GIF · 12 B · /p" {
		t.Fatalf("caption = %q", got)
	}
}

// T-IMG-23
func TestImageCellBox(t *testing.T) {
	cases := []struct {
		w, h, maxCols, maxRows int
		cols, rows             int
	}{
		{800, 400, 40, 12, 40, 10}, // 寬圖：受 maxCols 限制
		{400, 800, 40, 12, 12, 12}, // 高圖：受 maxRows 限制（cell 高 = 2 倍寬）
		{100, 100, 40, 12, 24, 12}, // 方圖
		{10, 10, 40, 12, 24, 12},   // 小圖也放大到框內（kitty 縮放）
		{800, 400, 40, 4, 16, 4},   // body 可用列數少
		{1000, 1, 40, 12, 40, 1},   // 極扁：至少 1 列
	}
	for _, item := range cases {
		cols, rows := imageCellBox(item.w, item.h, item.maxCols, item.maxRows)
		if cols != item.cols || rows != item.rows {
			t.Errorf("imageCellBox(%d,%d,%d,%d) = %dx%d, want %dx%d", item.w, item.h, item.maxCols, item.maxRows, cols, rows, item.cols, item.rows)
		}
	}
	if cols, rows := imageCellBox(0, 0, 40, 12); cols != 0 || rows != 0 {
		t.Errorf("unknown size = %dx%d", cols, rows)
	}
}
