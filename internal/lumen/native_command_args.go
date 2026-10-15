package lumen

import "strings"

// /model 的參數補全：輸入 "/model " 加上部分字串時，popup 顯示 backend 回報的 model，
// Tab 補全選取的 ID。只處理 /model；其他 command 沿用原本只補全 command 名稱的行為。

// modelCandidateCache 以 (backend, session) 為 key 快取 ModelOptions，避免 codex 的 model/list
// 在每次按鍵都送一次 RPC；換 backend、換 session 或 model 變動時失效。
type modelCandidateCache struct {
	backend Backend
	session string
	options []modelOption
}

func (u *ui) modelCandidates() []modelOption {
	backend := u.backend
	if backend == nil || !backend.Caps().ModelList {
		return nil
	}
	picker, ok := backend.(ModelPicker)
	if !ok {
		return nil
	}
	if cache := u.modelCache; cache != nil && cache.backend == backend && cache.session == u.thread.ID {
		return cache.options
	}
	options, _ := picker.ModelOptions()
	u.modelCache = &modelCandidateCache{backend: backend, session: u.thread.ID, options: options}
	return options
}

// modelArgInput 回傳輸入是否為 "/model <partial>"（partial 不含空白）。
func modelArgInput(value string) (partial string, ok bool) {
	if !strings.HasPrefix(value, "/") {
		return "", false
	}
	rest := strings.TrimPrefix(value, "/")
	end := strings.IndexAny(rest, " \t")
	if end < 0 || !strings.EqualFold(rest[:end], "model") {
		return "", false
	}
	partial = strings.TrimLeft(rest[end:], " \t")
	if strings.ContainsAny(partial, " \t\r\n") {
		return "", false
	}
	return partial, true
}

// modelArgItems 是 /model 參數 popup 的項目（argument=true；name 為 model ID）。
func (u *ui) modelArgItems() ([]slashCommand, bool) {
	partial, ok := modelArgInput(string(u.input))
	if !ok {
		return nil, false
	}
	query := strings.ToLower(partial)
	var items []slashCommand
	for _, option := range u.modelCandidates() {
		if query != "" && !strings.Contains(strings.ToLower(option.ID), query) && !strings.Contains(strings.ToLower(option.Name), query) {
			continue
		}
		description := option.Name
		if option.Description != "" {
			description = strings.Trim(description+" · "+option.Description, " ·")
		}
		if option.Current || option.ID == u.effectiveModel() {
			description = strings.TrimSpace("(current) " + description)
		}
		items = append(items, slashCommand{name: option.ID, description: description, argument: true})
	}
	return items, len(items) > 0
}

// popupLabel 是 popup 一列左側的文字：command 帶 "/"，參數候選不帶。
func (c slashCommand) popupLabel() string {
	if c.argument {
		return c.name
	}
	return "/" + c.name
}
