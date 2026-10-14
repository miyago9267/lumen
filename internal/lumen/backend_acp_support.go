package lumen

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	acpStderrMaxLines   = 200
	acpStderrMaxLineLen = 512
	acpStderrShowLines  = 5
)

// stderrRing 是 agent stderr 的有界緩衝：行數與單行長度都有上限，
// 不寫到 terminal 或檔案，只在連線中斷時取最後幾行顯示。
type stderrRing struct {
	mu      sync.Mutex
	lines   []string
	partial []byte
}

func (r *stderrRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			r.pushLocked(string(r.partial))
			r.partial = r.partial[:0]
			continue
		}
		if len(r.partial) < acpStderrMaxLineLen {
			r.partial = append(r.partial, b)
		}
	}
	return len(p), nil
}

func (r *stderrRing) pushLocked(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	r.lines = append(r.lines, line)
	if len(r.lines) > acpStderrMaxLines {
		r.lines = append(r.lines[:0], r.lines[len(r.lines)-acpStderrMaxLines:]...)
	}
}

// Tail 回傳最後 n 行（含尚未結束的最後一行）。
func (r *stderrRing) Tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	lines := append([]string(nil), r.lines...)
	if tail := strings.TrimSpace(string(r.partial)); tail != "" {
		lines = append(lines, tail)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func (r *stderrRing) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.lines)
}

// scrubACPEnv 從繼承的環境移除會改變 adapter 實際執行 binary 或認證來源的變數
// （CLAUDE_CODE_EXECUTABLE、ANTHROPIC_*）；其餘（HOME、PATH、USER、TMPDIR、LANG、TERM 等）照常繼承，
// claude CLI 自己的 keychain / login 認證不受影響。
func scrubACPEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		key := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			key = entry[:index]
		}
		if key == "CLAUDE_CODE_EXECUTABLE" || strings.HasPrefix(key, "ANTHROPIC_") {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// lumenStateDir 是 ACP subprocess 的固定工作目錄：${XDG_STATE_HOME:-~/.local/state}/lumen。
// 專案路徑只經由 session/new.cwd 與 session/load.cwd 傳遞。
func lumenStateDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "lumen"), nil
}
