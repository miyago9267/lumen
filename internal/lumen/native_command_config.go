package lumen

import (
	"fmt"
	"strings"
)

// 由 backend 的 select ConfigOption 自動產生的 slash command：/<option id>。
// 新 option 不需要在這裡加任何東西；mode / model / effort 與未知 category 走同一條路。

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

// configOptionDescription 是 catalog 內的一行說明：name，加上（與 name 不同的）description。
func configOptionDescription(option ConfigOption) string {
	name := oneLine(option.Name)
	if name == "" {
		name = option.ID
	}
	description := oneLine(option.Description)
	if description == "" || description == name {
		return name
	}
	return name + " · " + description
}

// configValuesTitle 是列出 value 時的標題；model 沿用既有的 "models"。
func configValuesTitle(id string) string {
	if strings.EqualFold(id, "model") {
		return "models"
	}
	return strings.ToLower(id) + " values"
}

// commandConfigOption 處理 /<id> [list | <value>]。切換只在使用者明確輸入 value 時發生。
func (u *ui) commandConfigOption(id, args string) error {
	option, ok := findConfigOptionByID(u.configOptions, id)
	if !ok {
		u.commandMessage("/"+strings.ToLower(id)+" is not available", muted)
		return nil
	}
	args = strings.TrimSpace(args)
	if args == "" || strings.EqualFold(args, "list") {
		lines := make([]string, 0, len(option.Values))
		for _, value := range option.Values {
			marker := "  "
			if value.Current || value.ID == option.Current {
				marker = "* "
			}
			line := marker + value.ID
			if name := oneLine(value.Name); name != "" && name != value.ID {
				line += " · " + name
			}
			if description := oneLine(value.Description); description != "" {
				line += " · " + description
			}
			lines = append(lines, line)
		}
		u.showCommandOutput(configValuesTitle(option.ID), lines)
		return nil
	}
	setter, ok := u.backend.(ConfigSetter)
	if !ok {
		u.commandMessage("/"+strings.ToLower(id)+" cannot be changed on backend "+u.backend.Name(), muted)
		return nil
	}
	value, err := setter.SetConfigOption(u.thread.ID, option.ID, args)
	if err != nil {
		return err
	}
	u.setConfigCurrent(option.ID, value)
	u.commandMessage(strings.ToLower(option.ID)+" · "+value, green)
	return nil
}

// setConfigCurrent 立即反映剛切換成功的值；之後 backend 的 EvConfigOptions 會整份取代。
func (u *ui) setConfigCurrent(id, value string) {
	updated := append([]ConfigOption(nil), u.configOptions...)
	for i := range updated {
		if updated[i].ID == id {
			updated[i] = updated[i].withCurrent(value)
		}
	}
	u.configOptions = updated
}

// configStatusLines 是 /status 的 options 段：每個 config option 的目前值（含沒有 command 的）。
func (u *ui) configStatusLines() []string {
	if len(u.configOptions) == 0 {
		return nil
	}
	lines := []string{"options"}
	for _, option := range u.configOptions {
		line := fmt.Sprintf("  %-11s %s", option.ID, knownValue(option.Current))
		if name := oneLine(option.Name); name != "" && name != option.ID {
			line += " · " + name
		}
		lines = append(lines, line)
	}
	return lines
}
