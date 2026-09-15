package main

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	mermaidNodePattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9_-]*)\s*\[([^\]]+)\]`)
	mermaidEdgePattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9_-]*)\s*(?:-->|---|==>|-.->)\s*([A-Za-z][A-Za-z0-9_-]*)`)
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

func inlineMarkdown(value string) string {
	value = strings.ReplaceAll(value, "**", "")
	value = strings.ReplaceAll(value, "__", "")
	var builder strings.Builder
	openCode := false
	for _, character := range value {
		if character == '`' {
			if openCode {
				builder.WriteString("⟧")
			} else {
				builder.WriteString("⟦")
			}
			openCode = !openCode
			continue
		}
		builder.WriteRune(character)
	}
	if openCode {
		builder.WriteString("⟧")
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
