package util

import (
	"regexp"
	"testing"
)

func TestNewID_Format(t *testing.T) {
	id := NewID()
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !re.MatchString(id) {
		t.Fatalf("NewID %q not a valid uuid v4", id)
	}
}