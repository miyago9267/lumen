package lumen

import (
	"fmt"
	"image"
	_ "image/gif"  // image.DecodeConfig 讀取尺寸
	_ "image/jpeg" // image.DecodeConfig 讀取尺寸
	_ "image/png"  // image.DecodeConfig 讀取尺寸
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// promptImage 是隨 prompt 附給 backend 的一張圖片（本機檔案）。
type promptImage struct {
	Path string
	MIME string
}

// imageAttachment 是 composer 中 `[Image #N]` chip 背後的圖片。
// Width / Height 為 0 表示無法解碼尺寸（例如 webp），預覽退回只顯示 caption。
type imageAttachment struct {
	N             int
	Path          string
	MIME          string
	Size          int64
	Width, Height int
	Temp          bool // Lumen 自己從剪貼簿寫出的 temp 檔；結束時刪除
	noPreview     bool // 傳輸失敗或太大：只顯示 caption
}

// clipboardContent 是一次剪貼簿讀取的結果。ImagePath 非空表示剪貼簿有圖片；
// 沒有圖片時 Text 是剪貼簿文字（可能為空）。
type clipboardContent struct {
	ImagePath string
	Temp      bool
	Text      string
}

func imageChip(n int) string { return "[Image #" + strconv.Itoa(n) + "]" }

type chipSpan struct{ start, end, n int } // [start, end) 為 chip 文字的 rune 範圍

// scanChips 找出 value 內所有 `[Image #N]` 形式的文字（不論有沒有 attachment）。
func scanChips(value []rune) []chipSpan {
	const prefix = "[Image #"
	var spans []chipSpan
	for index := 0; index < len(value); index++ {
		if value[index] != '[' || !strings.HasPrefix(string(value[index:min(index+len(prefix), len(value))]), prefix) {
			continue
		}
		digits := index + len(prefix)
		end := digits
		for end < len(value) && value[end] >= '0' && value[end] <= '9' {
			end++
		}
		if end == digits || end >= len(value) || value[end] != ']' {
			continue
		}
		n, err := strconv.Atoi(string(value[digits:end]))
		if err != nil {
			continue
		}
		spans = append(spans, chipSpan{start: index, end: end + 1, n: n})
		index = end
	}
	return spans
}

// chipSpans 只回傳有 attachment 的 chip；手打的 `[Image #9]` 只是普通文字。
func (u *ui) chipSpans() []chipSpan {
	var spans []chipSpan
	for _, span := range scanChips(u.input) {
		if u.images[span.n] != nil {
			spans = append(spans, span)
		}
	}
	return spans
}

// imagesInText 依文字中仍存在的 chip（出現順序、每張一次）回傳要附上的圖片。
// 附件由文字重新推導，所以 queue、cancel-and-send、歷史召回都自然帶著圖片。
func (u *ui) imagesInText(text string) []promptImage {
	var images []promptImage
	seen := map[int]bool{}
	for _, span := range scanChips([]rune(text)) {
		attachment := u.images[span.n]
		if attachment == nil || seen[span.n] {
			continue
		}
		seen[span.n] = true
		images = append(images, promptImage{Path: attachment.Path, MIME: attachment.MIME})
	}
	return images
}

func (u *ui) backendCaps() Capabilities {
	if u.backend != nil {
		return u.backend.Caps()
	}
	return u.lastCaps
}

// promptImagesFor 回傳 text 要附的圖片；text 含 chip 但 backend 不收圖片時回 error（呼叫端不得送出、不得清掉 input）。
func (u *ui) promptImagesFor(text string) ([]promptImage, error) {
	images := u.imagesInText(text)
	if len(images) > 0 && !u.backendCaps().ImagePrompt {
		return nil, fmt.Errorf("%s does not accept images; remove the [Image #N] chips to send", u.backendLabel())
	}
	return images, nil
}

// pasteClipboard 處理 Ctrl+V：剪貼簿有圖片就插入 chip；沒有就把剪貼簿文字當 paste 插入；
// 兩者皆無則顯示 muted 提示。backend 不收圖片時不插入任何東西。
func (u *ui) pasteClipboard() {
	reader := u.readClipboard
	if reader == nil {
		reader = readSystemClipboard
	}
	dir, err := u.imageTempDir()
	if err != nil {
		u.commandMessage("clipboard unavailable: "+err.Error(), muted)
		return
	}
	content, err := reader(dir)
	if err != nil {
		u.commandMessage("clipboard unavailable: "+err.Error(), muted)
		return
	}
	if content.ImagePath != "" {
		if !u.backendCaps().ImagePrompt {
			discardClipboardImage(content)
			u.commandMessage(u.backendLabel()+" does not accept images", muted)
			return
		}
		attachment, err := u.registerImage(content)
		if err != nil {
			discardClipboardImage(content)
			u.commandMessage("cannot attach image: "+err.Error(), muted)
			return
		}
		u.insertText(imageChip(attachment.N))
		return
	}
	if text := normalizePaste([]rune(content.Text)); text != "" {
		u.insertText(text)
		return
	}
	u.commandMessage("clipboard has no image", muted)
}

func discardClipboardImage(content clipboardContent) {
	if content.Temp && content.ImagePath != "" {
		os.Remove(content.ImagePath)
	}
}

// registerImage 驗證圖片、讀尺寸並配發下一個 N。
func (u *ui) registerImage(content clipboardContent) (*imageAttachment, error) {
	mime, err := detectImageMIME(content.ImagePath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(content.ImagePath)
	if err != nil {
		return nil, err
	}
	attachment := &imageAttachment{Path: content.ImagePath, MIME: mime, Size: info.Size(), Temp: content.Temp}
	if file, err := os.Open(content.ImagePath); err == nil {
		if config, _, err := image.DecodeConfig(file); err == nil {
			attachment.Width, attachment.Height = config.Width, config.Height
		}
		file.Close()
	}
	u.imageSeq++
	attachment.N = u.imageSeq
	if u.images == nil {
		u.images = map[int]*imageAttachment{}
	}
	u.images[attachment.N] = attachment
	return attachment, nil
}

// imageTempDir 回傳放剪貼簿圖片的 temp 目錄（第一次用到才建立，releaseImages 時移除）。
func (u *ui) imageTempDir() (string, error) {
	if u.imageDir == "" {
		dir, err := os.MkdirTemp("", "lumen-images-")
		if err != nil {
			return "", err
		}
		u.imageDir = dir
	}
	return u.imageDir, nil
}

// releaseImages 在 UI 結束時呼叫：釋放 terminal 端的圖片資料並刪除 Lumen 自己建立的 temp 檔。可重複呼叫。
func (u *ui) releaseImages() {
	u.freeKittyImages()
	for _, attachment := range u.images {
		if attachment.Temp {
			os.Remove(attachment.Path)
		}
	}
	if u.imageDir != "" {
		os.RemoveAll(u.imageDir)
		u.imageDir = ""
	}
}

// detectImageMIME 以檔案內容判斷 MIME（不信任副檔名），只接受 png/jpeg/gif/webp。
func detectImageMIME(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	head := make([]byte, 512)
	count, _ := file.Read(head)
	switch mime := http.DetectContentType(head[:count]); mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return mime, nil
	default:
		return "", fmt.Errorf("%s is not a png/jpeg/gif/webp image", filepath.Base(path))
	}
}

// imageMIMEByExtension 依副檔名回傳 MIME；不是支援的圖片副檔名則回傳空字串。
func imageMIMEByExtension(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return ""
}

// deleteInputRange 移除 [start, end) 並把游標放在 start。
func (u *ui) deleteInputRange(start, end int) {
	u.markInputEdited()
	u.input = append(u.input[:start], u.input[end:]...)
	u.cursor = start
}
