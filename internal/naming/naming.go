package naming

import (
	"context"
	"time"
)

// NamingRequest carries all data a FileNamer may need to produce a path.
type NamingRequest struct {
	JobID          string
	Timestamp      time.Time
	SuggestedTitle *string
	Metadata       map[string]any
	BasePath       string // normalised path prefix (trailing slash), applied by each namer
	Extension      string // e.g. ".md"
}

// FileNamer produces a repository-relative file path for a given request.
type FileNamer interface {
	Name(req NamingRequest) (string, error)
}

// PathChecker reports whether a path already exists in the target repository.
type PathChecker interface {
	Exists(ctx context.Context, repoPath string) (bool, error)
}
