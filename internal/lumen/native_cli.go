package lumen

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var nativeCodexSubcommands = map[string]bool{
	"agents": true, "exec": true, "e": true, "review": true, "login": true, "logout": true,
	"mcp": true, "plugin": true, "app-server": true, "remote-control": true, "app": true,
	"completion": true, "update": true, "doctor": true, "sandbox": true, "debug": true,
	"apply": true, "a": true, "resume": true, "queue": true, "archive": true, "delete": true,
	"migrate-rollouts": true, "unarchive": true, "fork": true, "cloud": true, "exec-server": true,
	"features": true, "help": true,
}

var nativeCodexValueFlags = map[string]bool{
	"-c": true, "--config": true, "--enable": true, "--disable": true,
	"--remote": true, "--remote-auth-token-env": true, "-i": true, "--image": true,
	"-m": true, "--model": true, "--local-provider": true, "-p": true, "--profile": true,
	"-s": true, "--sandbox": true, "-a": true, "--ask-for-approval": true, "-C": true,
	"--cd": true, "--add-dir": true,
}

var nativeCodexBooleanFlags = map[string]bool{
	"--oss": true, "--search": true, "--no-alt-screen": true, "--strict-config": true,
	"--worktree": true, "--approve-for-me": true,
	"--dangerously-bypass-approvals-and-sandbox": true, "--dangerously-bypass-approvals": true,
	"--dangerously-bypass-hook-trust": true,
}

func nativeCodexInvocation(args []string) ([]string, bool) {
	if len(args) == 0 {
		return nil, false
	}
	if args[0] == "codex" {
		return append([]string(nil), args[1:]...), true
	}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if nativeCodexSubcommands[argument] {
			return append([]string(nil), args...), true
		}
		if strings.HasPrefix(argument, "--") {
			name := argument
			if equal := strings.IndexByte(argument, '='); equal >= 0 {
				name = argument[:equal]
			}
			if nativeCodexValueFlags[name] {
				if !strings.Contains(argument, "=") {
					index++
					if index >= len(args) {
						return nil, false
					}
				}
				continue
			}
			if nativeCodexBooleanFlags[name] {
				continue
			}
			return nil, false
		}
		if strings.HasPrefix(argument, "-") {
			name := argument
			if len(argument) > 2 && argument[2] == '=' {
				name = argument[:2]
			}
			if nativeCodexValueFlags[name] {
				if !strings.Contains(argument, "=") {
					index++
					if index >= len(args) {
						return nil, false
					}
				}
				continue
			}
			return nil, false
		}
		return nil, false
	}
	return nil, false
}

func codexBinary() (string, error) {
	if binary := os.Getenv("LUMEN_CODEX_BIN"); binary != "" {
		return binary, nil
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		return "", fmt.Errorf("codex executable not found: %w", err)
	}
	return binary, nil
}

func runNativeCodex(args []string) int {
	binary, err := codexBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", appName, err)
		return 1
	}
	command := exec.Command(binary, args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "%s: run codex: %v\n", appName, err)
		return 1
	}
	return 0
}
