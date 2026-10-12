// Package openai implements the gostwriter LLM capabilities (transcription,
// title generation, filename suggestion) against any OpenAI-compatible Chat
// Completions endpoint, including an ai-proxy reverse proxy or api.openai.com.
// The wire protocol lives in the chat sub-package so additional providers can
// reuse it.
package openai

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"strings"

	"github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/llm"
	"github.com/jo-hoe/gostwriter/internal/llm/openai/chat"
)

//go:embed default_system_prompt.txt
var defaultSystemPrompt string

//go:embed default_instructions.txt
var defaultInstructions string

//go:embed title_system_prompt.txt
var defaultTitleSystemPrompt string

//go:embed title_instructions.txt
var defaultTitleInstructions string

//go:embed filename_instructions.txt
var defaultFilenameInstructions string

var (
	_ llm.Client          = (*Client)(nil)
	_ llm.TitleGenerator  = (*Client)(nil)
	_ llm.FilenameAdvisor = (*Client)(nil)
)

// providerName identifies conversations produced by this provider.
const providerName = "openai"

const (
	// titleMaxTokens bounds the title completion.
	titleMaxTokens = 64
	// filenameMaxTokens bounds the filename-slug completion.
	filenameMaxTokens = 32
)

// conversation is the openai implementation of llm.Conversation. It carries the
// full message history of a completed exchange so follow-up calls can continue
// it, letting the provider reuse cached prompt context.
type conversation struct {
	messages []chat.Message
}

func (*conversation) Provider() string { return providerName }

// Client implements llm.Client by calling an OpenAI-compatible chat endpoint.
type Client struct {
	chat        *chat.Client
	model       string
	system      string
	instr       string
	temperature *float32
	maxTokens   *int
}

// New creates an OpenAI provider client from configuration.
func New(cfg config.OpenAISettings) *Client {
	return &Client{
		chat:        chat.NewClient(cfg.BaseURL, cfg.APIKey, cfg.Timeout),
		model:       cfg.Model,
		system:      cfg.SystemPrompt,
		instr:       cfg.Instructions,
		temperature: optionalFloat32(cfg.Temperature),
		maxTokens:   optionalInt(cfg.MaxTokens),
	}
}

// TranscribeImage transcribes the document into Markdown and returns the
// completed conversation for follow-up calls.
func (c *Client) TranscribeImage(ctx context.Context, r io.Reader, mime string) (string, llm.Conversation, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", nil, fmt.Errorf("read document: %w", err)
	}
	if len(data) == 0 {
		return "", nil, fmt.Errorf("document is empty")
	}

	msgs := c.transcriptionMessages(chat.BuildDocumentPart(mime, data))
	req := c.newRequest(msgs, c.maxTokens)

	content, err := c.chat.Complete(ctx, req)
	if err != nil {
		return "", nil, err
	}

	conv := &conversation{
		messages: append(msgs, chat.Message{Role: chat.RoleAssistant, Content: content}),
	}
	return content, conv, nil
}

// GenerateTitle satisfies llm.TitleGenerator.
func (c *Client) GenerateTitle(ctx context.Context, markdown string) (string, error) {
	msgs := []chat.Message{
		{Role: chat.RoleSystem, Content: strings.TrimSpace(defaultTitleSystemPrompt)},
		{Role: chat.RoleUser, Content: strings.TrimSpace(defaultTitleInstructions) + "\n\n" + markdown},
	}
	req := c.newRequest(msgs, c.maxTokensOr(titleMaxTokens))

	title, err := c.chat.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	return cleanCompletion(title), nil
}

// SuggestFilename satisfies llm.FilenameAdvisor. It continues the transcription
// conversation with a follow-up instruction asking for a filename slug, letting
// the provider reuse the cached transcription context.
func (c *Client) SuggestFilename(ctx context.Context, conv llm.Conversation) (string, error) {
	prior, ok := conv.(*conversation)
	if !ok {
		return "", fmt.Errorf("openai: unsupported conversation type %T", conv)
	}

	msgs := append(prior.messages, chat.Message{
		Role:    chat.RoleUser,
		Content: strings.TrimSpace(defaultFilenameInstructions),
	})
	req := c.newRequest(msgs, c.maxTokensOr(filenameMaxTokens))

	slug, err := c.chat.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	return cleanCompletion(slug), nil
}

// transcriptionMessages builds the system + user messages for a transcription,
// applying configured prompt overrides or the embedded defaults.
func (c *Client) transcriptionMessages(userPart chat.Part) []chat.Message {
	sys := strings.TrimSpace(c.system)
	if sys == "" {
		sys = defaultSystemPrompt
	}
	instructions := strings.TrimSpace(c.instr)
	if instructions == "" {
		instructions = defaultInstructions
	}
	return []chat.Message{
		{Role: chat.RoleSystem, Content: sys},
		{Role: chat.RoleUser, Content: []chat.Part{
			{Type: chat.PartText, Text: &instructions},
			userPart,
		}},
	}
}

// newRequest assembles a chat.Request for the configured model, applying the
// optional temperature and the given max-tokens budget.
func (c *Client) newRequest(msgs []chat.Message, maxTokens *int) chat.Request {
	req := chat.Request{
		Model:    c.model,
		Messages: msgs,
		Stream:   false,
	}
	if c.temperature != nil {
		req.Temperature = c.temperature
	}
	if maxTokens != nil {
		req.MaxTokens = maxTokens
	}
	return req
}

// maxTokensOr returns the user-configured max-tokens override when set, else a
// pointer to the given default budget.
func (c *Client) maxTokensOr(def int) *int {
	if c.maxTokens != nil {
		return c.maxTokens
	}
	n := def
	return &n
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

func optionalInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}
