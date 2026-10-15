package lumen

import "strings"

// 參數補全：輸入 "/<command> " 加上部分字串時，popup 顯示該 command 的候選 value，Tab 補全選取的 value。
// 候選來源有兩種：backend 的 select ConfigOption（ACP；直接讀 UI 持有的清單，沒有 RPC），
// 以及 codex 靜態 catalog 的 /model（ModelPicker）。其他 command 沿用只補全 command 名稱的行為。

// modelCandidateCache 以 (backend, session) 為 key 快取 ModelOptions，避免 codex 的 model/list
// 在每次按鍵都送一次 RPC；換 backend、換 session 時失效。
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

// commandArgInput 回傳輸入是否為 "/<name> <partial>"（partial 不含空白）。
func commandArgInput(value string) (name, partial string, ok bool) {
	if !strings.HasPrefix(value, "/") {
		return "", "", false
	}
	rest := strings.TrimPrefix(value, "/")
	end := strings.IndexAny(rest, " \t")
	if end < 0 {
		return "", "", false
	}
	partial = strings.TrimLeft(rest[end:], " \t")
	if strings.ContainsAny(partial, " \t\r\n") {
		return "", "", false
	}
	return strings.ToLower(rest[:end]), partial, true
}

// argCandidates 是 command name 的候選 value 與目前值；command 不接受 value 補全時 ok=false。
func (u *ui) argCandidates(name string) (candidates []modelOption, current string, ok bool) {
	command, found := u.slashCommandByName(name)
	if !found || command.agent {
		return nil, "", false
	}
	if command.configID != "" {
		option, found := findConfigOptionByID(u.configOptions, command.configID)
		return option.Values, option.Current, found
	}
	if command.name == "model" {
		return u.modelCandidates(), u.effectiveModel(), true
	}
	return nil, "", false
}

// configCategory 回傳 command name 對應的 config option category；不是 option command 時回傳空字串。
func (u *ui) configCategory(name string) string {
	if command, found := u.slashCommandByName(name); found && command.configID != "" {
		if option, found := findConfigOptionByID(u.configOptions, command.configID); found {
			return option.Category
		}
	}
	return ""
}

// commandArgItems 是參數 popup 的項目（argument=true；name 為 value ID，parent 為 command）。
func (u *ui) commandArgItems() ([]slashCommand, bool) {
	name, partial, ok := commandArgInput(string(u.input))
	if !ok {
		return nil, false
	}
	candidates, current, ok := u.argCandidates(name)
	if !ok {
		return nil, false
	}
	query := strings.ToLower(partial)
	var items []slashCommand
	for _, option := range candidates {
		if query != "" && !strings.Contains(strings.ToLower(option.ID), query) && !strings.Contains(strings.ToLower(option.Name), query) {
			continue
		}
		description := oneLine(option.Name)
		if option.Description != "" {
			description = strings.Trim(description+" · "+oneLine(option.Description), " ·")
		}
		if option.Current || option.ID == current {
			description = strings.TrimSpace("(current) " + description)
		}
		items = append(items, slashCommand{name: option.ID, parent: name, description: description, argument: true})
	}
	return items, len(items) > 0
}

// exactArg 回傳 partial 是否已是 name 的某個完整 value（不分大小寫）。
func (u *ui) exactArg(name, partial string) bool {
	candidates, _, _ := u.argCandidates(name)
	for _, option := range candidates {
		if strings.EqualFold(option.ID, partial) {
			return true
		}
	}
	return false
}

// popupLabel 是 popup 一列左側的文字：command 帶 "/"，參數候選不帶。
func (c slashCommand) popupLabel() string {
	if c.argument {
		return c.name
	}
	return "/" + c.name
}
