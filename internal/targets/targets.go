package targets

import (
	"context"
	"time"
)

// Target is an output destination for a Markdown document.
type Target interface {
	Name() string
	Post(ctx context.Context, req TargetRequest) (TargetResult, error)
}

// TargetRequest contains data needed to post content.
type TargetRequest struct {
	JobID            string
	Markdown         string
	SuggestedTitle   *string
	Metadata         map[string]any
	Timestamp        time.Time
	FilenameTemplate string
	CommitTemplate   string
	BasePath         string
}

// TargetResult describes where the content landed.
type TargetResult struct {
	TargetName string
	Location   string
	Commit     string
}

// Registry holds initialized targets by name.
type Registry struct {
	byName map[string]Target
}

func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Target)}
}

func (r *Registry) Add(t Target) {
	r.byName[t.Name()] = t
}

func (r *Registry) Get(name string) (Target, bool) {
	t, ok := r.byName[name]
	return t, ok
}

func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.byName))
	for k := range r.byName {
		out = append(out, k)
	}
	return out
}