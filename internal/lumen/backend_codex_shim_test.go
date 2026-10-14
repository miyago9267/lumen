package lumen

import "encoding/json"

// 以下 helper 讓測試可以直接餵 codex 原始訊息：codex -> Event -> handleEvent。
// production 的 UI 只處理 normalized Event，不再認得 codex method name。

// codexShim 取得 shim 用的 codexBackend，並把 UI 目前的 thread / turn 同步給它。
func (u *ui) codexShim() *codexBackend {
	backend, ok := u.backend.(*codexBackend)
	if !ok {
		backend = newCodexBackend(nil, u.thread.ID)
	}
	backend.setState(u.thread.ID, u.turnID)
	return backend
}

func (u *ui) handleNotification(message rpcMessage) {
	translator := codexTranslator{threadID: u.thread.ID, turnID: u.turnID}
	for _, event := range translator.notification(message) {
		u.handleEvent(event)
	}
}

func (u *ui) handleItemStarted(raw json.RawMessage) {
	translator := codexTranslator{threadID: u.thread.ID, turnID: u.turnID}
	for _, event := range translator.itemStarted(raw) {
		u.handleEvent(event)
	}
}

func (u *ui) handleItemCompleted(raw json.RawMessage) {
	translator := codexTranslator{threadID: u.thread.ID, turnID: u.turnID}
	for _, event := range translator.itemCompleted(raw) {
		u.handleEvent(event)
	}
}

func (u *ui) handleServerRequest(request serverRequest) {
	for _, event := range u.codexShim().handleRequest(request) {
		u.handleEvent(event)
	}
}
