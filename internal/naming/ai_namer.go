package naming

import (
	"path/filepath"
	"strings"
)

// AINamer produces filenames of the form "YYYYMMdd-HHmmss-<slug><ext>".
//
// The slug is expected to arrive pre-sanitised in req.SuggestedSlug (the worker
// performs the LLM call and sanitisation so the filename call can reuse the
// transcription conversation's cached context). When no slug is supplied, the
// JobID is used as a fallback.
type AINamer struct {
	basePath  string
	extension string
}

// NewAINamer creates an AINamer that prepends basePath to every rendered name.
func NewAINamer(basePath, extension string) *AINamer {
	if extension == "" {
		extension = ".md"
	}
	return &AINamer{basePath: basePath, extension: extension}
}

func (n *AINamer) Name(req NamingRequest) (string, error) {
	slug := req.JobID
	if req.SuggestedSlug != nil && strings.TrimSpace(*req.SuggestedSlug) != "" {
		slug = strings.TrimSpace(*req.SuggestedSlug)
	}

	ts := req.Timestamp.UTC().Format("20060102-150405")
	name := ts + "-" + slug + n.extension
	if n.basePath != "" {
		name = filepath.Join(n.basePath, name)
	}
	return filepath.ToSlash(name), nil
}
