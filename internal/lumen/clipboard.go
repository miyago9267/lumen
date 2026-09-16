package lumen

import (
	"fmt"
	"os/exec"
	"strings"
)

func writeClipboard(value string) error {
	commands := []struct {
		name string
		args []string
	}{
		{name: "pbcopy"},
		{name: "wl-copy"},
		{name: "xclip", args: []string{"-selection", "clipboard"}},
		{name: "xsel", args: []string{"--clipboard", "--input"}},
	}

	var lastErr error
	for _, command := range commands {
		path, err := exec.LookPath(command.name)
		if err != nil {
			continue
		}
		process := exec.Command(path, command.args...)
		process.Stdin = strings.NewReader(value)
		if err := process.Run(); err == nil {
			return nil
		} else {
			lastErr = fmt.Errorf("%s: %w", command.name, err)
		}
	}
	if lastErr != nil {
		return fmt.Errorf("clipboard command failed: %w", lastErr)
	}
	return fmt.Errorf("no clipboard command found")
}
