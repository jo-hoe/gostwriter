package naming

import (
	"testing"
	"time"
)

func strPtr(s string) *string { return &s }

func TestAINamer_Name_UsesSuggestedSlug(t *testing.T) {
	n := NewAINamer("", ".md")
	got, err := n.Name(Request{
		JobID:         "job-1",
		Timestamp:     time.Date(2024, 11, 1, 14, 32, 0, 0, time.UTC),
		SuggestedSlug: strPtr("invoice-november"),
	})
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if got != "20241101-143200-invoice-november.md" {
		t.Fatalf("unexpected name: %q", got)
	}
}

func TestAINamer_Name_FallsBackToJobID(t *testing.T) {
	n := NewAINamer("", ".md")
	got, err := n.Name(Request{
		JobID:     "job-xyz",
		Timestamp: time.Date(2024, 11, 1, 14, 32, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if got != "20241101-143200-job-xyz.md" {
		t.Fatalf("unexpected name: %q", got)
	}
}

func TestAINamer_Name_BlankSuggestedSlugFallsBack(t *testing.T) {
	n := NewAINamer("", ".md")
	got, err := n.Name(Request{
		JobID:         "job-xyz",
		Timestamp:     time.Date(2024, 11, 1, 14, 32, 0, 0, time.UTC),
		SuggestedSlug: strPtr("   "),
	})
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if got != "20241101-143200-job-xyz.md" {
		t.Fatalf("unexpected name: %q", got)
	}
}

func TestAINamer_Name_AppliesBasePath(t *testing.T) {
	n := NewAINamer("notes/scanned", ".md")
	got, err := n.Name(Request{
		JobID:         "job-1",
		Timestamp:     time.Date(2024, 11, 1, 14, 32, 0, 0, time.UTC),
		SuggestedSlug: strPtr("invoice"),
	})
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if got != "notes/scanned/20241101-143200-invoice.md" {
		t.Fatalf("unexpected name: %q", got)
	}
}

func TestAINamer_Name_DefaultsExtension(t *testing.T) {
	n := NewAINamer("", "")
	got, err := n.Name(Request{
		JobID:         "job-1",
		Timestamp:     time.Date(2024, 11, 1, 14, 32, 0, 0, time.UTC),
		SuggestedSlug: strPtr("invoice"),
	})
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if got != "20241101-143200-invoice.md" {
		t.Fatalf("unexpected name: %q", got)
	}
}
