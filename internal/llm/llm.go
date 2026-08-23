package llm

import (
	"context"
	"io"
)

// TitleGenerator is an optional capability some LLM clients implement.
// The worker checks for it at runtime via a type-assertion on llm.Client.
type TitleGenerator interface {
	GenerateTitle(ctx context.Context, markdown string) (string, error)
}

// Client defines the capability to transcribe an image into Markdown.
type Client interface {
	// TranscribeImage reads an image from r (seek not required) with the given mime type
	// and returns a Markdown string.
	TranscribeImage(ctx context.Context, r io.Reader, mime string) (string, error)
}
