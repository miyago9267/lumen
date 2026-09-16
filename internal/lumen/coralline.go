package lumen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const corallineRenderTimeout = 750 * time.Millisecond

type corallineWorkspacePayload struct {
	CurrentDir string `json:"current_dir"`
}

type corallineModelPayload struct {
	DisplayName string `json:"display_name"`
}

type corallineEffortPayload struct {
	Level string `json:"level"`
}

type corallineContextUsagePayload struct {
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitempty"`
}

type corallineContextWindowPayload struct {
	UsedPercentage    float64                      `json:"used_percentage"`
	TotalInputTokens  int64                        `json:"total_input_tokens,omitempty"`
	TotalOutputTokens int64                        `json:"total_output_tokens,omitempty"`
	CurrentUsage      corallineContextUsagePayload `json:"current_usage,omitempty"`
}

type corallineStatusPayload struct {
	CWD           string                         `json:"cwd,omitempty"`
	Workspace     corallineWorkspacePayload      `json:"workspace"`
	Model         corallineModelPayload          `json:"model"`
	Effort        *corallineEffortPayload        `json:"effort,omitempty"`
	ContextWindow *corallineContextWindowPayload `json:"context_window,omitempty"`
}

type corallineRenderer interface {
	Render(width int, payload corallineStatusPayload) (string, error)
}

type corallineRendererFunc func(width int, payload corallineStatusPayload) (string, error)

func (fn corallineRendererFunc) Render(width int, payload corallineStatusPayload) (string, error) {
	return fn(width, payload)
}

type shellCorallineRenderer struct {
	path    string
	shell   string
	timeout time.Duration
}

func newCorallineRenderer() corallineRenderer {
	path := corallineStatuslinePath()
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	shell, err := exec.LookPath("bash")
	if err != nil {
		return nil
	}
	return &shellCorallineRenderer{path: path, shell: shell, timeout: corallineRenderTimeout}
}

func corallineStatuslinePath() string {
	if configured := strings.TrimSpace(os.Getenv("LUMEN_CORALLINE_STATUSLINE")); configured != "" {
		if strings.EqualFold(configured, "off") || configured == "-" {
			return ""
		}
		return configured
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "coralline", "statusline.sh")
}

func (renderer *shellCorallineRenderer) Render(width int, payload corallineStatusPayload) (string, error) {
	if renderer == nil || renderer.path == "" {
		return "", fmt.Errorf("coralline renderer is unavailable")
	}
	input, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal coralline payload: %w", err)
	}
	timeout := renderer.timeout
	if timeout <= 0 {
		timeout = corallineRenderTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, renderer.shell, renderer.path)
	command.Stdin = bytes.NewReader(input)
	command.Env = corallineEnvironment(width)
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("coralline renderer timeout")
		}
		return "", fmt.Errorf("coralline renderer failed")
	}
	return sanitizeCorallineOutput(string(output)), nil
}

func corallineEnvironment(width int) []string {
	environment := os.Environ()
	filtered := make([]string, 0, len(environment)+2)
	for _, entry := range environment {
		if strings.HasPrefix(entry, "CORALLINE_NO_SAMPLE=") || strings.HasPrefix(entry, "COLUMNS=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered,
		"CORALLINE_NO_SAMPLE=1",
		"COLUMNS="+strconv.Itoa(maxInt(1, width)),
	)
}

func (u *ui) corallinePayload() corallineStatusPayload {
	cwd := valueOr(u.cwd, u.thread.CWD)
	payload := corallineStatusPayload{
		CWD:       cwd,
		Workspace: corallineWorkspacePayload{CurrentDir: cwd},
		Model:     corallineModelPayload{DisplayName: u.effectiveModel()},
	}
	if effort := strings.TrimSpace(u.effectiveEffort()); effort != "" {
		payload.Effort = &corallineEffortPayload{Level: effort}
	}
	if u.usage.hasContextWindow && u.usage.modelContextWindow > 0 {
		usedPercentage := float64(u.usage.totalTokens) * 100 / float64(u.usage.modelContextWindow)
		if usedPercentage < 0 {
			usedPercentage = 0
		}
		if usedPercentage > 100 {
			usedPercentage = 100
		}
		payload.ContextWindow = &corallineContextWindowPayload{
			UsedPercentage:    usedPercentage,
			TotalInputTokens:  u.usage.inputTokens,
			TotalOutputTokens: u.usage.outputTokens,
			CurrentUsage: corallineContextUsagePayload{
				CacheReadInputTokens:     u.usage.cacheReadInputTokens,
				CacheCreationInputTokens: u.usage.cacheCreationInputTokens,
			},
		}
	}
	return payload
}

func (u *ui) refreshCoralline(width int) {
	if u.corallineRenderer == nil {
		u.corallineLine = ""
		u.corallineSnapshot = ""
		u.corallineError = ""
		return
	}
	payload := u.corallinePayload()
	encoded, err := json.Marshal(payload)
	if err != nil {
		u.corallineLine = ""
		u.corallineError = err.Error()
		return
	}
	snapshot := strconv.Itoa(width) + "\x00" + string(encoded)
	if snapshot == u.corallineSnapshot {
		return
	}
	u.corallineSnapshot = snapshot
	line, err := u.corallineRenderer.Render(width, payload)
	if err != nil {
		u.corallineLine = ""
		// Do not cache a failed render. The next invalidation can recover from a
		// transient renderer timeout or a temporarily unavailable dependency.
		u.corallineSnapshot = ""
		u.corallineError = err.Error()
		return
	}
	u.corallineLine = sanitizeCorallineOutput(line)
	u.corallineError = ""
}

func sanitizeCorallineOutput(value string) string {
	var output strings.Builder
	lastWasSpace := false
	for index := 0; index < len(value); {
		if value[index] == 0x1b {
			sequence, next, ok := corallineEscape(value, index)
			if ok {
				if sequence != "" {
					output.WriteString(sequence)
				}
				index = next
				continue
			}
			index++
			continue
		}
		character, size := utf8.DecodeRuneInString(value[index:])
		if character == utf8.RuneError && size == 1 {
			index++
			continue
		}
		index += size
		switch character {
		case '\n', '\r', '\t':
			if output.Len() > 0 && !lastWasSpace {
				output.WriteString("  ·  ")
				lastWasSpace = true
			}
		default:
			if character >= 0x20 && character != 0x7f {
				output.WriteRune(character)
				lastWasSpace = character == ' '
			}
		}
	}
	return strings.TrimSpace(output.String())
}

func corallineEscape(value string, offset int) (string, int, bool) {
	if offset+1 >= len(value) {
		return "", len(value), true
	}
	if value[offset+1] == ']' {
		for index := offset + 2; index < len(value); index++ {
			if value[index] == '\a' {
				return "", index + 1, true
			}
			if value[index] == 0x1b && index+1 < len(value) && value[index+1] == '\\' {
				return "", index + 2, true
			}
		}
		return "", len(value), true
	}
	if value[offset+1] != '[' {
		return "", minInt(len(value), offset+2), true
	}
	for index := offset + 2; index < len(value); index++ {
		character := value[index]
		if character < 0x40 || character > 0x7e {
			continue
		}
		if character != 'm' {
			return "", index + 1, true
		}
		return value[offset : index+1], index + 1, true
	}
	return "", len(value), true
}

func truncateCoralline(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if displayWidth(value) <= width {
		return value
	}
	target := width - 1
	if target < 0 {
		target = 0
	}
	var output strings.Builder
	visible := 0
	for index := 0; index < len(value); {
		if value[index] == 0x1b {
			sequence, next, ok := corallineEscape(value, index)
			if ok {
				if sequence != "" {
					output.WriteString(sequence)
				}
				index = next
				continue
			}
			index++
			continue
		}
		character, size := utf8.DecodeRuneInString(value[index:])
		if character == utf8.RuneError && size == 1 {
			index++
			continue
		}
		characterWidth := runeDisplayWidth(character)
		if visible+characterWidth > target {
			break
		}
		output.WriteRune(character)
		visible += characterWidth
		index += size
	}
	output.WriteRune('…')
	output.WriteString("\x1b[0m")
	return output.String()
}

func padCoralline(value string, width int) string {
	value = truncateCoralline(value, width)
	padding := width - displayWidth(value)
	if padding < 0 {
		padding = 0
	}
	return value + "\x1b[0m" + strings.Repeat(" ", padding)
}
