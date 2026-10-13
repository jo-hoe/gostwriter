// Package targets defines the target interface and request/result types for posting transcriptions.
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
	JobID         string
	Markdown      string
	SuggestedSlug *string
	Metadata      map[string]any
	Timestamp     time.Time
	FilenameTemplate string
	CommitTemplate   string
	BasePath         string
	// Original, when non-nil, is the source document to archive alongside the
	// transcription. Targets that do not support archiving ignore it.
	Original *OriginalDocument
}

// OriginalDocument is the source upload to be archived in the target.
type OriginalDocument struct {
	Content   []byte
	Extension string // leading dot, e.g. ".pdf", ".png"
	MimeType  string
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

// NewRegistry creates an empty target Registry.
func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Target)}
}

// Add registers a target by its name.
func (r *Registry) Add(t Target) {
	r.byName[t.Name()] = t
}

// Get returns the target with the given name, and whether it was found.
func (r *Registry) Get(name string) (Target, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// Names returns the names of all registered targets.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.byName))
	for k := range r.byName {
		out = append(out, k)
	}
	return out
}
