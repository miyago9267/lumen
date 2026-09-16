package lumen

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

const (
	initialReconnectDelay = 250 * time.Millisecond
	maxReconnectDelay     = 4 * time.Second
)

type reconnectResult struct {
	client *appServer
	thread threadSummary
	err    error
}

type terminalLifecycle struct {
	fd        int
	out       io.Writer
	enter     string
	exit      string
	makeRaw   func(int) (*term.State, error)
	restore   func(int, *term.State) error
	state     *term.State
	entered   bool
	closeOnce sync.Once
	closeErr  error
}

func newTerminalLifecycle(fd int, out io.Writer, enter, exit string) *terminalLifecycle {
	return &terminalLifecycle{
		fd:      fd,
		out:     out,
		enter:   enter,
		exit:    exit,
		makeRaw: term.MakeRaw,
		restore: func(fd int, state *term.State) error {
			return term.Restore(fd, state)
		},
	}
}

func newFullscreenTerminalLifecycle(fd int, out io.Writer) *terminalLifecycle {
	return newTerminalLifecycle(
		fd,
		out,
		"\x1b[?1049h\x1b[?25l\x1b[>1u\x1b[?1003h\x1b[?1006h",
		"\x1b[?1006l\x1b[?1003l\x1b[<u\x1b[?25h\x1b[?1049l\r\n",
	)
}

func newInlineTerminalLifecycle(fd int, out io.Writer) *terminalLifecycle {
	return newTerminalLifecycle(fd, out, "\x1b[?25l", "\x1b[?25h\n")
}

func (l *terminalLifecycle) Enter() error {
	if l.makeRaw == nil {
		l.makeRaw = term.MakeRaw
	}
	if l.restore == nil {
		l.restore = func(fd int, state *term.State) error {
			return term.Restore(fd, state)
		}
	}
	state, err := l.makeRaw(l.fd)
	if err != nil {
		return err
	}
	if l.out != nil && l.enter != "" {
		if _, err := io.WriteString(l.out, l.enter); err != nil {
			_ = l.restore(l.fd, state)
			return err
		}
	}
	l.state = state
	l.entered = true
	return nil
}

func (l *terminalLifecycle) Close() error {
	l.closeOnce.Do(func() {
		if !l.entered {
			return
		}
		if l.out != nil && l.exit != "" {
			if _, err := io.WriteString(l.out, l.exit); err != nil {
				l.closeErr = err
			}
		}
		if l.restore != nil && l.state != nil {
			if err := l.restore(l.fd, l.state); err != nil && l.closeErr == nil {
				l.closeErr = err
			}
		}
	})
	return l.closeErr
}

func (u *ui) recoverUI(context string, fn func()) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("%s panic: %v", context, value)
		}
	}()
	fn()
	return nil
}

func (u *ui) safeUIError(context string, fn func() error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("%s panic: %v", context, value)
		}
	}()
	return fn()
}

func (u *ui) reportUIError(err error) {
	if err == nil {
		return
	}
	message := "UI recovered · " + err.Error()
	if u.screenMode {
		u.screenAdd(screenError, message)
		return
	}
	u.printError(errors.New(message))
}

func reconnectDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	delay := initialReconnectDelay
	for index := 0; index < attempt && delay < maxReconnectDelay; index++ {
		delay *= 2
	}
	if delay > maxReconnectDelay {
		return maxReconnectDelay
	}
	return delay
}

func connectionErrorText(err error) string {
	if err == nil {
		return "app-server closed"
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "app-server connection lost"
	}
	return truncateDisplay(sanitizeText(message), 160)
}

func (u *ui) hasTranscriptContent() bool {
	if len(u.thread.Turns) > 0 {
		return true
	}
	for _, entry := range u.transcript.entries {
		switch entry.kind {
		case screenUser, screenAssistant, screenTool, screenPlan:
			return true
		}
	}
	return false
}

func canStartFreshThreadAfterResume(err error, hasTranscriptContent bool) bool {
	if err == nil || hasTranscriptContent {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "no rollout found")
}

func (u *ui) markConnectionLost(err error) {
	u.client = nil
	u.connectionError = connectionErrorText(err)
	u.connectionStatusEntry = ""
	u.reconnectAttempt = 0
	u.reconnectRetryPending = false
	u.approval = nil
	u.inputRequest = nil
	u.busy = false
	u.turnID = ""
	u.assistant = false
	u.tool = false
	u.toolOutput = false
	u.reasoningActive = false
	u.reasoningShown = false
	u.reasoningFrame = 0
	u.cancelAndSend = ""
	u.endScreenTurn()

	message := "connection lost · reconnecting"
	if u.screenMode {
		u.showWelcome = false
		u.screenSetConnectionStatus(screenError, message)
		return
	}
	u.printError(errors.New(message))
	u.renderPrompt()
}

func (u *ui) markReconnectFailed(err error, attempt int) {
	u.connectionError = connectionErrorText(err)
	u.reconnectAttempt = attempt
	u.reconnectRetryPending = true
	message := fmt.Sprintf("reconnecting · attempt %d · retrying in %s", attempt, reconnectDelay(attempt))
	if u.screenMode {
		u.screenSetConnectionStatus(screenWarning, message)
		return
	}
	u.printStatus(message, yellow)
	u.renderPrompt()
}

func (u *ui) adoptReconnected(client *appServer, thread threadSummary) {
	u.client = client
	if thread.ID != "" {
		u.thread = thread
	}
	u.replayThreadHistory(thread)
	u.cwd = valueOr(u.thread.CWD, u.cwd)
	u.branch = gitBranch(u.cwd)
	u.connectionError = ""
	u.connectionStatusEntry = ""
	u.reconnectAttempt = 0
	u.reconnectRetryPending = false
	u.approval = nil
	u.inputRequest = nil
	u.busy = false
	u.turnID = ""
	u.assistant = false
	u.tool = false
	u.toolOutput = false
	u.reasoningActive = false
	u.reasoningShown = false
	u.reasoningFrame = 0
	u.endScreenTurn()
	u.showWelcome = false
	if u.screenMode {
		u.screenAdd(screenStatus, "reconnected · "+shortID(u.thread.ID))
		return
	}
	u.printStatus("reconnected · "+shortID(u.thread.ID), green)
	u.renderPrompt()
}

func (u *ui) replayThreadHistory(thread threadSummary) {
	start := 0
	if len(thread.Turns) > 20 {
		start = len(thread.Turns) - 20
	}
	for _, turn := range thread.Turns[start:] {
		turnKey := turn.ID
		if turnKey == "" {
			turnKey = u.newScreenTurnKey()
		}
		for _, item := range turn.Items {
			u.renderScreenHistoryItemForTurnMode(item, turnKey, turn.ID, true)
		}
	}
}

func (u *ui) screenSetConnectionStatus(kind screenBlockKind, text string) {
	if !u.screenMode {
		return
	}
	text = strings.TrimRight(sanitizeText(text), "\n")
	if text == "" {
		return
	}
	if index := u.transcript.indexByID(u.connectionStatusEntry); index >= 0 {
		entry := &u.transcript.entries[index]
		if entry.turnKey == "" {
			entry.kind = kind
			entry.role = kind
			entry.text = text
			u.syncScreenProjection()
			u.clearSelection()
			return
		}
	}
	u.screenAddForTurn(kind, text, "", "")
	if len(u.transcript.entries) > 0 {
		u.connectionStatusEntry = u.transcript.entries[len(u.transcript.entries)-1].id
	}
}

func (u *ui) reconnectAsync(done <-chan struct{}) <-chan reconnectResult {
	result := make(chan reconnectResult)
	factory := u.serverFactory
	if factory == nil {
		factory = startAppServer
	}
	snapshot := reconnectSnapshot{
		threadID:             u.thread.ID,
		cwd:                  u.cwd,
		overrides:            u.overrides,
		hasTranscriptContent: u.hasTranscriptContent(),
	}
	go func() {
		outcome := reconnectWithSnapshot(factory, snapshot, done)
		select {
		case result <- outcome:
		case <-done:
			if outcome.client != nil {
				outcome.client.Close()
			}
		}
	}()
	return result
}
