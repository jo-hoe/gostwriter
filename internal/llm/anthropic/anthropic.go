// Package anthropic implements the gostwriter LLM capabilities (transcription
// and filename suggestion) against the native Anthropic Messages API, served by
// api.anthropic.com or a reverse proxy exposing an /anthropic route.
//
// The Messages API accepts PDFs natively as "document" content blocks, so no
// rasterization is required.
// The wire protocol lives in the messages sub-package.
package anthropic

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"strings"

	"github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/llm"
	"github.com/jo-hoe/gostwriter/internal/llm/anthropic/messages"
)

//go:embed default_system_prompt.txt
var defaultSystemPrompt string

//go:embed default_instructions.txt
var defaultInstructions string

//go:embed filename_instructions.txt
var defaultFilenameInstructions string

var (
	_ llm.Client          = (*Client)(nil)
	_ llm.FilenameAdvisor = (*Client)(nil)
)

// providerName identifies conversations produced by this provider.
const providerName = "anthropic"

const (
	// filenameMaxTokens bounds the filename-slug completion.
	filenameMaxTokens = 32
)

// conversation is the anthropic implementation of llm.Conversation. It carries
// the full message history of a completed exchange so follow-up calls can
// continue it.
type conversation struct {
	messages []messages.Message
}

func (*conversation) Provider() string { return providerName }

// Client implements llm.Client by calling the Anthropic Messages endpoint.
type Client struct {
	messages    *messages.Client
	model       string
	system      string
	instr       string
	temperature *float32
	maxTokens   int
}

// New creates an Anthropic provider client from configuration.
func New(cfg config.AnthropicSettings) *Client {
	return &Client{
		messages:    messages.NewClient(cfg.BaseURL, cfg.APIKey, cfg.Timeout),
		model:       cfg.Model,
		system:      cfg.SystemPrompt,
		instr:       cfg.Instructions,
		temperature: optionalFloat32(cfg.Temperature),
		maxTokens:   cfg.MaxTokens,
	}
}

// TranscribeImage transcribes the document into Markdown and returns the
// completed conversation for follow-up calls. PDFs are sent natively as
// "document" blocks; images as "image" blocks.
func (c *Client) TranscribeImage(ctx context.Context, r io.Reader, mime string) (string, llm.Conversation, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", nil, fmt.Errorf("read document: %w", err)
	}
	if len(data) == 0 {
		return "", nil, fmt.Errorf("document is empty")
	}

	msgs := c.transcriptionMessages(messages.BuildDocumentBlock(mime, data))
	req := c.newRequest(msgs, c.maxTokens)

	content, err := c.messages.Complete(ctx, req)
	if err != nil {
		return "", nil, fmt.Errorf("transcribe: %w", err)
	}

	conv := &conversation{
		messages: append(msgs, messages.Message{
			Role:    messages.RoleAssistant,
			Content: []messages.Block{messages.TextBlock(content)},
		}),
	}
	return content, conv, nil
}

// SuggestFilename satisfies llm.FilenameAdvisor. It continues the transcription
// conversation with a follow-up instruction asking for a filename slug.
func (c *Client) SuggestFilename(ctx context.Context, conv llm.Conversation) (string, error) {
	prior, ok := conv.(*conversation)
	if !ok {
		return "", fmt.Errorf("anthropic: unsupported conversation type %T", conv)
	}

	msgs := make([]messages.Message, len(prior.messages)+1)
	copy(msgs, prior.messages)
	msgs[len(prior.messages)] = messages.Message{
		Role:    messages.RoleUser,
		Content: []messages.Block{messages.TextBlock(strings.TrimSpace(defaultFilenameInstructions))},
	}
	req := c.newRequest(msgs, c.maxTokensOr(filenameMaxTokens))

	slug, err := c.messages.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("suggest filename: %w", err)
	}
	return cleanCompletion(slug), nil
}

// transcriptionMessages builds the user message for a transcription, applying
// the configured instruction override or the embedded default. The Anthropic
// system prompt is a top-level request field (set in newRequest), not a message.
func (c *Client) transcriptionMessages(docBlock messages.Block) []messages.Message {
	instructions := strings.TrimSpace(c.instr)
	if instructions == "" {
		instructions = defaultInstructions
	}
	return []messages.Message{
		{Role: messages.RoleUser, Content: []messages.Block{
			messages.TextBlock(instructions),
			docBlock,
		}},
	}
}

// newRequest assembles a messages.Request for the configured model, applying the
// system prompt, optional temperature and the given max-tokens budget.
func (c *Client) newRequest(msgs []messages.Message, maxTokens int) messages.Request {
	sys := strings.TrimSpace(c.system)
	if sys == "" {
		sys = defaultSystemPrompt
	}
	req := messages.Request{
		Model:     c.model,
		System:    sys,
		Messages:  msgs,
		MaxTokens: maxTokens,
	}
	if c.temperature != nil {
		req.Temperature = c.temperature
	}
	return req
}

// maxTokensOr returns the user-configured max-tokens override when set, else the
// given default budget.
func (c *Client) maxTokensOr(def int) int {
	if c.maxTokens > 0 {
		return c.maxTokens
	}
	return def
}

// cleanCompletion trims whitespace and surrounding quotes the model sometimes adds.
func cleanCompletion(s string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"'`))
}

func optionalFloat32(v float32) *float32 {
	if v == 0 {
		return nil
	}
	return &v
}
