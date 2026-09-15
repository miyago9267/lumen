package main

import (
	"fmt"
	"strings"
)

const maxQueuedPrompts = 32

func (u *ui) queuePrompt() error {
	value := strings.TrimSpace(string(u.input))
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "/btw") {
		return u.rejectAsidePrompt()
	}
	if len(u.queuedPrompts) >= maxQueuedPrompts {
		return fmt.Errorf("prompt queue is full (%d)", maxQueuedPrompts)
	}
	u.queuedPrompts = append(u.queuedPrompts, value)
	u.rememberPrompt(value)
	u.input = nil
	u.cursor = 0
	if u.screenMode {
		u.screenAdd(screenStatus, "queued · "+truncateDisplay(sanitizeText(value), 96))
	} else {
		u.printStatus("queued · "+sanitizeText(value), cyan)
	}
	return nil
}

func (u *ui) cancelAndSendPrompt() error {
	value := strings.TrimSpace(string(u.input))
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "/btw") {
		return u.rejectAsidePrompt()
	}
	u.cancelAndSend = value
	u.rememberPrompt(value)
	u.input = nil
	u.cursor = 0
	if u.screenMode {
		u.screenAdd(screenStatus, "cancel-and-send · interrupting current turn")
	} else {
		u.printStatus("cancel-and-send · interrupting current turn", yellow)
	}
	if u.turnID == "" {
		return nil
	}
	if u.client == nil {
		return fmt.Errorf("cancel-and-send requires app-server")
	}
	return u.client.Interrupt(u.thread.ID, u.turnID)
}

func (u *ui) rejectAsidePrompt() error {
	u.input = nil
	u.cursor = 0
	message := "aside is not wired to an independent turn yet · use Dashboard to open a side session"
	if u.screenMode {
		u.screenAdd(screenWarning, message)
	} else {
		u.printStatus(message, yellow)
	}
	return nil
}

func (u *ui) startNextPrompt() {
	value := u.cancelAndSend
	u.cancelAndSend = ""
	if value == "" && len(u.queuedPrompts) > 0 {
		value = u.queuedPrompts[0]
		u.queuedPrompts = u.queuedPrompts[1:]
	}
	if value == "" {
		return
	}
	if u.client == nil {
		u.printError(fmt.Errorf("queued prompt requires app-server"))
		return
	}
	if err := u.submit(value); err != nil {
		u.printError(err)
	}
}
