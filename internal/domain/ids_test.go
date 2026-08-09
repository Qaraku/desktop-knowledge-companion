package domain

import (
	"regexp"
	"testing"
	"time"
)

func TestNewIDUsesUUIDv7Shape(t *testing.T) {
	id, err := NewID(time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
		t.Fatalf("unexpected UUIDv7: %s", id)
	}
}
