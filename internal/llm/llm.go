package llm

import (
	"context"
	"io"
)

// Conversation is an opaque handle to a completed LLM exchange. Implementations
// carry whatever internal state is needed to continue the conversation (e.g.
// message history for prompt-cache reuse). Callers hold it and pass it back to
// follow-up calls, but must not inspect its contents.
//
// Provider reports the name of the LLM provider that produced the conversation.
// It lets a FilenameAdvisor verify a Conversation originated from the same
// provider before continuing it.
type Conversation interface {
	Provider() string
}

// Client defines the capability to transcribe an image into Markdown.
type Client interface {
	// TranscribeImage reads a document from r (seek not required) with the
	// given mime type and returns the transcribed Markdown together with the
	// completed Conversation, which follow-up calls may continue.
	TranscribeImage(ctx context.Context, r io.Reader, mime string) (string, Conversation, error)
}

// TitleGenerator is an optional capability some LLM clients implement.
// The worker checks for it at runtime via a type-assertion on llm.Client.
type TitleGenerator interface {
	GenerateTitle(ctx context.Context, markdown string) (string, error)
}

// FilenameAdvisor is an optional capability some LLM clients implement: it
// suggests a filename slug by continuing a prior transcription Conversation,
// which lets the provider reuse cached context from the transcription call.
type FilenameAdvisor interface {
	SuggestFilename(ctx context.Context, conv Conversation) (string, error)
}
