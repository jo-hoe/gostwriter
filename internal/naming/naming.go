// Package naming provides file naming strategies for transcription output.
package naming

import (
	"context"
	"time"
)

// Request carries all data a FileNamer may need to produce a path.
type Request struct {
	JobID         string
	Timestamp     time.Time
	SuggestedSlug *string
	Metadata      map[string]any
	BasePath      string // normalised path prefix (trailing slash), applied by each namer
	Extension     string // e.g. ".md"
}

// FileNamer produces a repository-relative file path for a given request.
type FileNamer interface {
	Name(req Request) (string, error)
}

// PathChecker reports whether a path already exists in the target repository.
type PathChecker interface {
	Exists(ctx context.Context, repoPath string) (bool, error)
}
