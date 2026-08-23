package naming

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// TitleNamer uses the SuggestedTitle (slugified) as the filename base,
// falling back to JobID when no title is available.
type TitleNamer struct {
	basePath  string
	extension string
}

func NewTitleNamer(basePath, extension string) *TitleNamer {
	if extension == "" {
		extension = ".md"
	}
	return &TitleNamer{basePath: basePath, extension: extension}
}

func (n *TitleNamer) Name(req NamingRequest) (string, error) {
	base := req.JobID
	if req.SuggestedTitle != nil && strings.TrimSpace(*req.SuggestedTitle) != "" {
		base = slugify(*req.SuggestedTitle)
	}
	name := base + n.extension
	if n.basePath != "" {
		name = filepath.Join(n.basePath, name)
	}
	return filepath.ToSlash(name), nil
}

var reNonWord = regexp.MustCompile(`-{2,}`)

// slugify converts a title to a lowercase, filename-safe slug.
func slugify(s string) string {
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
