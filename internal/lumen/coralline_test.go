package lumen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShellCorallineRendererPassesPayloadAndRenderEnvironment(t *testing.T) {
	directory := t.TempDir()
	statusline := filepath.Join(directory, "statusline.sh")
	script := `#!/bin/sh
input=$(cat)
if [ "${CORALLINE_NO_SAMPLE:-}" != "1" ] || [ "${COLUMNS:-}" != "100" ]; then
  exit 9
fi
case "$input" in
  *'"current_dir":"/tmp/project"'*) ;;
  *) exit 8 ;;
esac
case "$input" in
  *'"display_name":"gpt-test"'*) ;;
  *) exit 8 ;;
esac
printf '\033[38;5;81mcoralline shell\033[0m\n'
`
	if err := os.WriteFile(statusline, []byte(script), 0o700); err != nil {
		t.Fatalf("write statusline fixture: %v", err)
	}

	renderer := &shellCorallineRenderer{path: statusline, shell: "/bin/sh", timeout: time.Second}
	got, err := renderer.Render(100, corallineStatusPayload{
		CWD:       "/tmp/project",
		Workspace: corallineWorkspacePayload{CurrentDir: "/tmp/project"},
		Model:     corallineModelPayload{DisplayName: "gpt-test"},
	})
	if err != nil {
		t.Fatalf("render coralline fixture: %v", err)
	}
	if !strings.Contains(got, "coralline shell") || !strings.Contains(got, "\x1b[38;5;81m") {
		t.Fatalf("renderer output = %q", got)
	}
}

func TestCorallineRefreshRetriesFailureAndCachesSuccess(t *testing.T) {
	calls := 0
	failed := true
	frontend := &ui{
		corallineRenderer: corallineRendererFunc(func(width int, payload corallineStatusPayload) (string, error) {
			calls++
			if failed {
				return "", os.ErrNotExist
			}
			return "coralline cached", nil
		}),
	}

	frontend.refreshCoralline(80)
	frontend.refreshCoralline(80)
	if calls != 2 {
		t.Fatalf("failed renderer calls = %d, want retry on next invalidation", calls)
	}

	failed = false
	frontend.refreshCoralline(80)
	frontend.refreshCoralline(80)
	if calls != 3 {
		t.Fatalf("successful renderer calls = %d, want cached success", calls)
	}
	if frontend.corallineLine != "coralline cached" || frontend.corallineError != "" {
		t.Fatalf("renderer state = line %q, error %q", frontend.corallineLine, frontend.corallineError)
	}
}

func TestCorallinePayloadIncludesUsageBreakdown(t *testing.T) {
	frontend := &ui{
		cwd:    "/tmp/project",
		thread: threadSummary{Model: "gpt-test", ReasoningEffort: "high"},
		usage: threadUsage{
			totalTokens:              250,
			inputTokens:              100,
			outputTokens:             150,
			cacheReadInputTokens:     40,
			cacheCreationInputTokens: 20,
			modelContextWindow:       1000,
			hasContextWindow:         true,
		},
	}

	payload := frontend.corallinePayload()
	if payload.CWD != "/tmp/project" || payload.Workspace.CurrentDir != "/tmp/project" {
		t.Fatalf("cwd payload = %#v", payload)
	}
	if payload.Model.DisplayName != "gpt-test" || payload.Effort == nil || payload.Effort.Level != "high" {
		t.Fatalf("model/effort payload = %#v", payload)
	}
	if payload.ContextWindow == nil {
		t.Fatal("context payload is nil")
	}
	if payload.ContextWindow.UsedPercentage != 25 {
		t.Fatalf("context percentage = %v, want 25", payload.ContextWindow.UsedPercentage)
	}
	if payload.ContextWindow.TotalInputTokens != 100 || payload.ContextWindow.TotalOutputTokens != 150 {
		t.Fatalf("token breakdown = %#v", payload.ContextWindow)
	}
	if payload.ContextWindow.CurrentUsage.CacheReadInputTokens != 40 || payload.ContextWindow.CurrentUsage.CacheCreationInputTokens != 20 {
		t.Fatalf("cache breakdown = %#v", payload.ContextWindow.CurrentUsage)
	}
}

func TestCorallineDockReservesCursorRow(t *testing.T) {
	frontend := &ui{
		screenMode:    true,
		input:         []rune("hello"),
		cursor:        5,
		corallineLine: "coralline status",
	}

	row, column, ok := frontend.screenCursorPosition(100, 16)
	if !ok || row != 13 || column != 10 {
		t.Fatalf("cursor position = (%d, %d, %t), want (13, 10, true)", row, column, ok)
	}
}
