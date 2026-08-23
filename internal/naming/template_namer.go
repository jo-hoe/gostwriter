package naming

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"
)

// TemplateNamer renders a Go text/template to produce the filename.
// Available template variables: .JobID, .Timestamp, .SuggestedTitle, .Metadata
type TemplateNamer struct {
	tpl      *template.Template
	basePath string
}

// NewTemplateNamer parses tplStr (falling back to defaultTpl when empty) and
// returns a TemplateNamer that prepends basePath to every rendered name.
func NewTemplateNamer(tplStr, defaultTpl, basePath string) (*TemplateNamer, error) {
	s := strings.TrimSpace(tplStr)
	if s == "" {
		s = defaultTpl
	}
	tpl, err := template.New("filename").Parse(s)
	if err != nil {
		return nil, fmt.Errorf("parse filename template: %w", err)
	}
	return &TemplateNamer{tpl: tpl, basePath: basePath}, nil
}

func (n *TemplateNamer) Name(req NamingRequest) (string, error) {
	data := map[string]any{
		"JobID":          req.JobID,
		"Timestamp":      req.Timestamp,
		"SuggestedTitle": req.SuggestedTitle,
		"Metadata":       req.Metadata,
	}
	var buf bytes.Buffer
	if err := n.tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render filename: %w", err)
	}
	name := strings.TrimSpace(buf.String())
	if name == "" {
		ext := req.Extension
		if ext == "" {
			ext = ".md"
		}
		name = fmt.Sprintf("%s-%s%s", req.Timestamp.Format("20060102-150405"), req.JobID, ext)
	}
	if n.basePath != "" {
		name = filepath.Join(n.basePath, name)
	}
	return filepath.ToSlash(name), nil
}
