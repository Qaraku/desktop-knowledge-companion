package organizer

import (
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

type Candidate struct {
	Ordinal   int      `json:"ordinal"`
	Content   string   `json:"content"`
	TitlePath []string `json:"title_path"`
}

func Organize(kind, content string) ([]Candidate, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, fmt.Errorf("content is empty")
	}
	switch kind {
	case "text":
		return []Candidate{{Ordinal: 0, Content: trimmed}}, nil
	case "markdown":
		return organizeMarkdown([]byte(content))
	default:
		return nil, fmt.Errorf("unsupported input kind %q", kind)
	}
}

func organizeMarkdown(source []byte) ([]Candidate, error) {
	document := goldmark.DefaultParser().Parse(text.NewReader(source))
	paths := make([]string, 6)
	var candidates []Candidate
	err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch current := node.(type) {
		case *ast.Heading:
			title := strings.TrimSpace(string(current.Text(source)))
			if title == "" {
				return ast.WalkContinue, nil
			}
			level := current.Level
			paths[level-1] = title
			for index := level; index < len(paths); index++ {
				paths[index] = ""
			}
		case *ast.Paragraph:
			body := strings.TrimSpace(string(current.Text(source)))
			if body == "" {
				return ast.WalkContinue, nil
			}
			path := make([]string, 0, len(paths))
			for _, item := range paths {
				if item != "" {
					path = append(path, item)
				}
			}
			candidates = append(candidates, Candidate{Ordinal: len(candidates), Content: body, TitlePath: path})
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk markdown: %w", err)
	}
	if len(candidates) == 0 {
		return []Candidate{{Ordinal: 0, Content: strings.TrimSpace(string(source))}}, nil
	}
	return candidates, nil
}
