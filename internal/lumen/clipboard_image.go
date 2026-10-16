package lumen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const clipboardTimeout = 5 * time.Second

// readSystemClipboard 讀系統剪貼簿。有圖片時把它寫成 dir 底下的 temp 檔（Finder 檔案則直接用原檔）；
// 沒有圖片時回傳剪貼簿文字。只靠外部指令（osascript / wl-paste / xclip），不使用 cgo。
func readSystemClipboard(dir string) (clipboardContent, error) {
	if runtime.GOOS == "darwin" {
		return readDarwinClipboard(dir)
	}
	return readLinuxClipboard(dir)
}

func runClipboardCommand(name string, args ...string) ([]byte, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
	defer cancel()
	var stdout bytes.Buffer
	command := exec.CommandContext(ctx, path, args...)
	command.Stdout = &stdout
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return stdout.Bytes(), nil
}

func readDarwinClipboard(dir string) (clipboardContent, error) {
	info, err := runClipboardCommand("osascript", "-e", "clipboard info")
	if err != nil {
		return clipboardContent{}, err
	}
	kinds := string(info)
	switch {
	case strings.Contains(kinds, "furl"):
		// Finder 複製的檔案：是圖片檔就直接用，不複製也不刪。
		out, err := runClipboardCommand("osascript", "-e", "POSIX path of (the clipboard as «class furl»)")
		if path := strings.TrimSpace(string(out)); err == nil && imageMIMEByExtension(path) != "" {
			if _, err := detectImageMIME(path); err == nil {
				return clipboardContent{ImagePath: path}, nil
			}
		}
	case strings.Contains(kinds, "PNGf") || strings.Contains(kinds, "TIFF"):
		file, err := os.CreateTemp(dir, "clip-*.png")
		if err != nil {
			return clipboardContent{}, err
		}
		path := file.Name()
		file.Close()
		script := fmt.Sprintf("set theData to (the clipboard as «class PNGf»)\n"+
			"set fp to open for access POSIX file %q with write permission\n"+
			"set eof of fp to 0\nwrite theData to fp\nclose access fp", path)
		if _, err := runClipboardCommand("osascript", "-e", script); err == nil {
			if _, err := detectImageMIME(path); err == nil {
				return clipboardContent{ImagePath: path, Temp: true}, nil
			}
		}
		os.Remove(path)
	}
	text, err := runClipboardCommand("pbpaste")
	if err != nil {
		return clipboardContent{}, err
	}
	return clipboardContent{Text: string(text)}, nil
}

func readLinuxClipboard(dir string) (clipboardContent, error) {
	found := false
	for _, command := range [][]string{
		{"wl-paste", "-t", "image/png"},
		{"xclip", "-selection", "clipboard", "-t", "image/png", "-o"},
	} {
		if _, err := exec.LookPath(command[0]); err != nil {
			continue
		}
		found = true
		data, err := runClipboardCommand(command[0], command[1:]...)
		if err != nil || len(data) == 0 {
			continue
		}
		file, err := os.CreateTemp(dir, "clip-*.png")
		if err != nil {
			return clipboardContent{}, err
		}
		path := file.Name()
		_, writeErr := file.Write(data)
		file.Close()
		if writeErr == nil {
			if _, err := detectImageMIME(path); err == nil {
				return clipboardContent{ImagePath: path, Temp: true}, nil
			}
		}
		os.Remove(path)
	}
	for _, command := range [][]string{
		{"wl-paste", "--no-newline"},
		{"xclip", "-selection", "clipboard", "-o"},
		{"xsel", "--clipboard", "--output"},
	} {
		if _, err := exec.LookPath(command[0]); err != nil {
			continue
		}
		found = true
		if text, err := runClipboardCommand(command[0], command[1:]...); err == nil {
			return clipboardContent{Text: string(text)}, nil
		}
	}
	if !found {
		return clipboardContent{}, errors.New("no clipboard command found")
	}
	return clipboardContent{}, nil
}
