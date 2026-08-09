package store

import "strings"

func contains(value, fragment string) bool {
	return strings.Contains(value, fragment)
}
