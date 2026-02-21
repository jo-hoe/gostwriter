package targets

import (
	"context"
	"testing"
	"time"
)

type dummyTarget struct{ name string }

func (d *dummyTarget) Name() string { return d.name }
func (d *dummyTarget) Post(ctx context.Context, req TargetRequest) (TargetResult, error) {
	return TargetResult{
		TargetName: d.name,
		Location:   "loc",
		Commit:     "deadbeef",
	}, nil
}

func TestRegistry_AddGetNames(t *testing.T) {
	reg := NewRegistry()
	if len(reg.Names()) != 0 {
		t.Fatalf("expected empty registry")
	}
	tg := &dummyTarget{name: "t1"}
	reg.Add(tg)

	if _, ok := reg.Get("t1"); !ok {
		t.Fatalf("expected to get target t1")
	}
	names := reg.Names()
	if len(names) != 1 || names[0] != "t1" {
		t.Fatalf("names mismatch: %+v", names)
	}

	// Ensure request/response types compile and behave
	req := TargetRequest{
		JobID:            "job",
		Markdown:         "md",
		SuggestedTitle:   nil,
		Metadata:         map[string]any{"k": "v"},
		Timestamp:        time.Now(),
		FilenameTemplate: "{{ .JobID }}.md",
		CommitTemplate:   "Add {{ .JobID }}",
		BasePath:         "docs/",
	}
	if _, err := tg.Post(context.Background(), req); err != nil {
		t.Fatalf("dummy post returned error: %v", err)
	}
}
