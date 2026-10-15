package lumen

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	mermaidNodePattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9_-]*)\s*\[([^\]]+)\]`)
	mermaidEdgePattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9_-]*)\s*(?:\[[^\]]+\])?\s*(?:-->|---|==>|-.->)\s*([A-Za-z][A-Za-z0-9_-]*)`)
)

func markdownContentLines(value string) []screenContentLine {
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return nil
	}
	lines := strings.Split(value, "\n")
	content := make([]screenContentLine, 0, len(lines))
	inFence := false
	fenceLanguage := ""
	var mermaidLines []string
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") {
			if !inFence {
				inFence = true
				fenceLanguage = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				if strings.EqualFold(fenceLanguage, "mermaid") {
					mermaidLines = nil
					content = append(content, screenContentLine{kind: screenDiagram, text: "mermaid · flow preview"})
				} else {
					content = append(content, screenContentLine{kind: screenCode, text: "code · " + valueOr(fenceLanguage, "text")})
				}
				continue
			}
			if strings.EqualFold(fenceLanguage, "mermaid") {
				content = append(content, renderMermaidPreview(mermaidLines)...)
			} else {
				content = append(content, screenContentLine{kind: screenCode, text: "end code"})
			}
			inFence = false
			fenceLanguage = ""
			mermaidLines = nil
			continue
		}
		if inFence {
			if strings.EqualFold(fenceLanguage, "mermaid") {
				mermaidLines = append(mermaidLines, raw)
			} else {
				content = append(content, screenContentLine{kind: screenCode, text: raw})
			}
			continue
		}
		if trimmed == "" {
			content = append(content, screenContentLine{kind: screenAssistant, text: ""})
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "#"):
			heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			content = append(content, screenContentLine{kind: screenInfo, text: "▸ " + inlineMarkdown(heading)})
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			content = append(content, screenContentLine{kind: screenAssistant, text: "• " + inlineMarkdown(strings.TrimSpace(trimmed[2:]))})
		case strings.HasPrefix(trimmed, ">"):
			content = append(content, screenContentLine{kind: screenAssistant, text: "│ " + inlineMarkdown(strings.TrimSpace(strings.TrimPrefix(trimmed, ">")))})
		default:
			content = append(content, screenContentLine{kind: screenAssistant, text: inlineMarkdown(raw)})
		}
	}
	if inFence && strings.EqualFold(fenceLanguage, "mermaid") {
		content = append(content, renderMermaidPreview(mermaidLines)...)
	}
	return content
}

// codeSpanOpen / codeSpanClose 是 inline code span 的內部標記（Unicode private use，不會被 terminal 顯示）。
// 版面（wrap、截斷）把它們當成零寬字元；NO_COLOR 時改成 backtick 並佔 1 欄。標記在最後一刻
// （screenStyledLine）才轉成 ANSI，複製文字則一律去掉，因此不會有裝飾字元外洩。
// sanitizeText 會剝除輸入中本來就有的標記，內容無法偽造 span。
const (
	codeSpanOpen  = '\ue000'
	codeSpanClose = '\ue001'
)

func isCodeSpanMark(character rune) bool {
	return character == codeSpanOpen || character == codeSpanClose
}

// codeSpanBackticks 回報目前是否沒有顏色可用（NO_COLOR），此時 code span 以 backtick 呈現。
func codeSpanBackticks() bool {
	return cyan == ""
}

func inlineMarkdown(value string) string {
	value = strings.ReplaceAll(value, "**", "")
	value = strings.ReplaceAll(value, "__", "")
	var builder strings.Builder
	openCode := false
	for _, character := range value {
		if character == '`' {
			if openCode {
				builder.WriteRune(codeSpanClose)
			} else {
				builder.WriteRune(codeSpanOpen)
			}
			openCode = !openCode
			continue
		}
		builder.WriteRune(character)
	}
	if openCode {
		builder.WriteRune(codeSpanClose)
	}
	return builder.String()
}

// expandCodeSpans 把標記轉成最終輸出：有顏色時 open -> code style、close -> restore（該行原本的 style）；
// 沒有顏色時轉成 backtick。行尾仍開著的 span 由呼叫端附加的 reset 收尾。
func expandCodeSpans(value, restore string) string {
	if !strings.ContainsAny(value, string([]rune{codeSpanOpen, codeSpanClose})) {
		return value
	}
	var builder strings.Builder
	for _, character := range value {
		switch character {
		case codeSpanOpen:
			if codeSpanBackticks() {
				builder.WriteByte('`')
			} else {
				builder.WriteString(cyan)
			}
		case codeSpanClose:
			if codeSpanBackticks() {
				builder.WriteByte('`')
			} else {
				builder.WriteString(restore)
			}
		default:
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

// plainCodeSpans 回傳可複製的純文字：有顏色時去掉標記，NO_COLOR 時保留畫面上的 backtick。
func plainCodeSpans(value string) string {
	var builder strings.Builder
	for _, character := range value {
		switch {
		case !isCodeSpanMark(character):
			builder.WriteRune(character)
		case codeSpanBackticks():
			builder.WriteByte('`')
		}
	}
	return builder.String()
}

func renderMermaidPreview(lines []string) []screenContentLine {
	labels := make(map[string]string)
	for _, line := range lines {
		for _, match := range mermaidNodePattern.FindAllStringSubmatch(line, -1) {
			labels[match[1]] = strings.TrimSpace(match[2])
		}
	}
	content := make([]screenContentLine, 0, len(lines))
	for _, line := range lines {
		if match := mermaidEdgePattern.FindStringSubmatch(line); len(match) == 3 {
			from := valueOr(labels[match[1]], match[1])
			to := valueOr(labels[match[2]], match[2])
			content = append(content, screenContentLine{kind: screenDiagram, text: fmt.Sprintf("%s ──▶ %s", from, to)})
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(strings.ToLower(trimmed), "flowchart ") || strings.HasPrefix(strings.ToLower(trimmed), "graph ") {
			continue
		}
		content = append(content, screenContentLine{kind: screenDiagram, text: trimmed})
	}
	if len(content) == 0 {
		content = append(content, screenContentLine{kind: screenDiagram, text: "(no flow nodes)"})
	}
	return content
}
