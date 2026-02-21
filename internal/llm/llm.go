package llm

import (
	"context"
	"io"
)

// Client defines the capability to transcribe an image into Markdown.
type Client interface {
	// TranscribeImage reads an image from r (seek not required) with the given mime type
	// and returns a Markdown string.
	TranscribeImage(ctx context.Context, r io.Reader, mime string) (string, error)
}