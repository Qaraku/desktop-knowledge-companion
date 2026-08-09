package organizer

import "testing"

func TestOrganizeMarkdownPreservesParagraphOrderAndHeadingPath(t *testing.T) {
	candidates, err := Organize("markdown", "# First\n\nOne.\n\n## Nested\n\nTwo.\n\n# Second\n\nThree.")
	if err != nil {
		t.Fatalf("organize markdown: %v", err)
	}
	if len(candidates) != 3 {
		t.Fatalf("candidate count = %d", len(candidates))
	}
	if candidates[0].Ordinal != 0 || candidates[0].Content != "One." || len(candidates[0].TitlePath) != 1 || candidates[0].TitlePath[0] != "First" {
		t.Fatalf("unexpected first candidate: %#v", candidates[0])
	}
	if candidates[1].Ordinal != 1 || candidates[1].TitlePath[1] != "Nested" {
		t.Fatalf("unexpected nested candidate: %#v", candidates[1])
	}
	if candidates[2].Ordinal != 2 || len(candidates[2].TitlePath) != 1 || candidates[2].TitlePath[0] != "Second" {
		t.Fatalf("unexpected final candidate: %#v", candidates[2])
	}
}
