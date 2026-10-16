package lumen

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"sort"
	"strings"
)

const (
	kittyChunkSize     = 4096
	previewMaxCols     = 40
	previewMaxRows     = 12
	previewMaxPNGBytes = 10 << 20 // 超過就只顯示 caption，避免一次塞幾十 MB 進 pty
	previewColumn      = 3        // 預覽左緣（1-based），與 composer 邊框對齊
)

// detectKittyGraphics 判斷 terminal 是否支援 kitty graphics protocol。tmux 不轉送，所以不算。
func detectKittyGraphics(getenv func(string) string) bool {
	switch strings.ToLower(getenv("TERM_PROGRAM")) {
	case "ghostty", "kitty", "wezterm":
		return true
	}
	term := strings.ToLower(getenv("TERM"))
	return strings.Contains(term, "kitty") || strings.Contains(term, "ghostty")
}

// kittyTransmitSeq 以 direct、chunked base64 傳輸 PNG（f=100），image id 為 id；q=2 不要 terminal 回應。
func kittyTransmitSeq(id int, data []byte) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	var builder strings.Builder
	for first := true; ; first = false {
		chunk := encoded
		more := 0
		if len(chunk) > kittyChunkSize {
			chunk, encoded = chunk[:kittyChunkSize], chunk[kittyChunkSize:]
			more = 1
		}
		controls := fmt.Sprintf("m=%d", more)
		if first {
			controls = fmt.Sprintf("a=t,f=100,i=%d,q=2,%s", id, controls)
		}
		fmt.Fprintf(&builder, "\x1b_G%s;%s\x1b\\", controls, chunk)
		if more == 0 {
			return builder.String()
		}
	}
}

// kittyPlaceSeq 在目前 cursor 位置放 image id（placement 1）並縮放到 cols x rows 個 cell，C=1 不移動 cursor。
// 同 image id + placement id 再次 place 會取代舊 placement，不需要重傳資料。
func kittyPlaceSeq(id, cols, rows int) string {
	return fmt.Sprintf("\x1b_Ga=p,i=%d,p=1,c=%d,r=%d,C=1,q=2\x1b\\", id, cols, rows)
}

// kittyDeleteSeq 刪除 placement；free 為 true 時一併釋放圖片資料。
func kittyDeleteSeq(id int, free bool) string {
	if free {
		return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id)
	}
	return fmt.Sprintf("\x1b_Ga=d,d=i,i=%d,p=1,q=2\x1b\\", id)
}

// imageCellBox 把 w x h 像素的圖縮進 maxCols x maxRows 個 cell，保持長寬比；
// 假設 cell 高度是寬度的 2 倍。尺寸未知回傳 0, 0。
func imageCellBox(w, h, maxCols, maxRows int) (int, int) {
	if w <= 0 || h <= 0 || maxCols <= 0 || maxRows <= 0 {
		return 0, 0
	}
	cols := maxCols
	rows := (cols*h + w) / (2 * w) // round(cols * h / (2 * w))
	if rows > maxRows {
		rows = maxRows
		cols = (rows*2*w + h/2) / h // round(rows * 2 * w / h)
	}
	return max(cols, 1), max(rows, 1)
}

func imageFormatLabel(mime string) string {
	return strings.ToUpper(strings.TrimPrefix(mime, "image/"))
}

func humanSize(size int64) string {
	switch {
	case size < 1024:
		return fmt.Sprintf("%d B", size)
	case size < 1<<20:
		return fmt.Sprintf("%d KB", (size+512)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	}
}

// imageCaption 是預覽上方那一行：`Image #N · WxH · PNG · 230 KB · <path>`，未知欄位省略。
func imageCaption(attachment *imageAttachment) string {
	parts := []string{fmt.Sprintf("Image #%d", attachment.N)}
	if attachment.Width > 0 && attachment.Height > 0 {
		parts = append(parts, fmt.Sprintf("%dx%d", attachment.Width, attachment.Height))
	}
	parts = append(parts, imageFormatLabel(attachment.MIME), humanSize(attachment.Size), attachment.Path)
	return strings.Join(parts, " · ")
}

// imagePreview 是 composer 上方的圖片預覽；rows == 0 表示只顯示 caption。
type imagePreview struct {
	attachment *imageAttachment
	caption    string
	cols, rows int
}

// screenPreview 回傳目前要顯示的預覽（沒有 focus 的 chip 時為 nil）。
func (u *ui) screenPreview(width, height int) *imagePreview {
	_, bodyRows := u.screenComposerAndBodyRows(width, height)
	return u.previewFor(width, bodyRows)
}

// previewFor 的可見規則：screen mode、沒有 approval / input request / dashboard / backend picker，
// 且游標在有 attachment 的 chip 上或剛好在它後面。
func (u *ui) previewFor(width, bodyRows int) *imagePreview {
	number, ok := u.focusedChip()
	if !ok {
		return nil
	}
	attachment := u.images[number]
	preview := &imagePreview{attachment: attachment, caption: imageCaption(attachment)}
	if u.kittyGraphics && !attachment.noPreview && attachment.MIME != "image/webp" {
		// 至少留一列 transcript，圖片最多 40x12 cell。
		maxRows := min(previewMaxRows, bodyRows-1)
		maxCols := min(previewMaxCols, width-6)
		if maxRows >= 2 && maxCols >= 4 {
			preview.cols, preview.rows = imageCellBox(attachment.Width, attachment.Height, maxCols, maxRows)
		}
	}
	return preview
}

// focusedChip 回傳游標所在的 chip：優先 `start < cursor <= end`（在 chip 上或剛好在後面），其次游標恰在 chip 起點。
func (u *ui) focusedChip() (int, bool) {
	if !u.screenMode || u.approval != nil || u.inputRequest != nil || u.dashboard || u.backendPicker {
		return 0, false
	}
	spans := u.chipSpans()
	for _, span := range spans {
		if span.start < u.cursor && u.cursor <= span.end {
			return span.n, true
		}
	}
	for _, span := range spans {
		if span.start == u.cursor {
			return span.n, true
		}
	}
	return 0, false
}

// syncImagePreview 在 frame 畫完之後對齊 terminal 端的圖片：預覽消失就刪 placement；
// 顯示中則（第一次才傳輸資料）每次重新 place。
func (u *ui) syncImagePreview(width, height int) {
	if !u.kittyGraphics || u.out == nil {
		return
	}
	_, bodyRows := u.screenComposerAndBodyRows(width, height)
	preview := u.previewFor(width, bodyRows)
	want := 0
	if preview != nil && preview.rows > 0 {
		want = preview.attachment.N
	}
	if u.kittyPlaced != 0 && u.kittyPlaced != want {
		fmt.Fprint(u.out, kittyDeleteSeq(u.kittyPlaced, false))
		u.kittyPlaced = 0
	}
	if want == 0 {
		return
	}
	if !u.kittySent[want] {
		data, err := previewPNG(preview.attachment)
		if err != nil {
			preview.attachment.noPreview = true
			return
		}
		fmt.Fprint(u.out, kittyTransmitSeq(want, data))
		if u.kittySent == nil {
			u.kittySent = map[int]bool{}
		}
		u.kittySent[want] = true
	}
	// 預覽佔 transcript body 的最後 rows 列；body 從第 3 列開始（header + rule 之後）。
	row := 3 + bodyRows - preview.rows
	fmt.Fprintf(u.out, "\x1b[%d;%dH%s", row, previewColumn, kittyPlaceSeq(want, preview.cols, preview.rows))
	u.kittyPlaced = want
}

// freeKittyImages 釋放所有已傳輸的圖片（UI 結束時）。
func (u *ui) freeKittyImages() {
	if len(u.kittySent) == 0 {
		return
	}
	ids := make([]int, 0, len(u.kittySent))
	for id := range u.kittySent {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	if u.out != nil {
		for _, id := range ids {
			fmt.Fprint(u.out, kittyDeleteSeq(id, true))
		}
	}
	u.kittySent = nil
	u.kittyPlaced = 0
}

// previewPNG 取得可傳給 kitty 的 PNG bytes：PNG 原樣使用，jpeg / gif 以 stdlib 轉成 PNG。
func previewPNG(attachment *imageAttachment) ([]byte, error) {
	data, err := os.ReadFile(attachment.Path)
	if err != nil {
		return nil, err
	}
	if attachment.MIME != "image/png" {
		decoded, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		var buffer bytes.Buffer
		if err := png.Encode(&buffer, decoded); err != nil {
			return nil, err
		}
		data = buffer.Bytes()
	}
	if len(data) > previewMaxPNGBytes {
		return nil, fmt.Errorf("image too large for inline preview (%s)", humanSize(int64(len(data))))
	}
	return data, nil
}
