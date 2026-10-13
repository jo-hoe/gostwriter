package naming

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
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

var reNonWord = regexp.MustCompile(`-{2,}`)

// Slugify converts a string to a lowercase, filename-safe slug.
func Slugify(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune('-')
		}
	}
	result := reNonWord.ReplaceAllString(b.String(), "-")
	result = strings.Trim(result, "-")
	if result == "" {
		return "untitled"
	}
	return result
}
