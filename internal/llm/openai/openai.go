// Package openai implements the gostwriter LLM capabilities (transcription and
// filename suggestion) against any OpenAI-compatible Responses API endpoint,
// including a reverse proxy or api.openai.com. The Responses API transcribes
// both images and PDFs natively (via "input_image" and "input_file" parts).
// The wire protocol lives in the responses sub-package so additional providers
// can reuse it.
package openai

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"strings"

	"github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/llm"
	"github.com/jo-hoe/gostwriter/internal/llm/openai/responses"
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
const providerName = "openai"

const (
	// filenameMaxTokens bounds the filename-slug completion.
	filenameMaxTokens = 32
)

// conversation is the openai implementation of llm.Conversation. It carries the
// full input history of a completed exchange so follow-up calls can continue
// it, letting the provider reuse cached prompt context.
type conversation struct {
	input []responses.Item
}

func (*conversation) Provider() string { return providerName }

// Client implements llm.Client by calling an OpenAI-compatible Responses endpoint.
type Client struct {
	responses   *responses.Client
	model       string
	system      string
	instr       string
	temperature *float32
	maxTokens   *int
}

// New creates an OpenAI provider client from configuration.
func New(cfg config.OpenAISettings) *Client {
	return &Client{
		responses:   responses.NewClient(cfg.BaseURL, cfg.APIKey, cfg.Timeout),
		model:       cfg.Model,
		system:      cfg.SystemPrompt,
		instr:       cfg.Instructions,
		temperature: optionalFloat32(cfg.Temperature),
		maxTokens:   optionalInt(cfg.MaxTokens),
	}
}

// TranscribeImage transcribes the document into Markdown and returns the
// completed conversation for follow-up calls. Both images and PDFs are
// supported via the Responses API.
func (c *Client) TranscribeImage(ctx context.Context, r io.Reader, mime string) (string, llm.Conversation, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", nil, fmt.Errorf("read document: %w", err)
	}
	if len(data) == 0 {
		return "", nil, fmt.Errorf("document is empty")
	}

	input := c.transcriptionInput(responses.BuildDocumentPart(mime, data))
	req := c.newRequest(input, c.maxTokens)

	content, err := c.responses.Complete(ctx, req)
	if err != nil {
		return "", nil, fmt.Errorf("transcribe: %w", err)
	}

	conv := &conversation{
		input: append(input, assistantItem(content)),
	}
	return content, conv, nil
}

// SuggestFilename satisfies llm.FilenameAdvisor. It continues the transcription
// conversation with a follow-up instruction asking for a filename slug, letting
// the provider reuse the cached transcription context.
func (c *Client) SuggestFilename(ctx context.Context, conv llm.Conversation) (string, error) {
	prior, ok := conv.(*conversation)
	if !ok {
		return "", fmt.Errorf("openai: unsupported conversation type %T", conv)
	}

	input := make([]responses.Item, len(prior.input)+1)
	copy(input, prior.input)
	input[len(prior.input)] = responses.Item{
		Role:    responses.RoleUser,
		Content: []responses.Part{responses.TextPart(strings.TrimSpace(defaultFilenameInstructions))},
	}
	req := c.newRequest(input, c.maxTokensOr(filenameMaxTokens))

	slug, err := c.responses.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("suggest filename: %w", err)
	}
	return cleanCompletion(slug), nil
}

// transcriptionInput builds the system + user input items for a transcription,
// applying configured prompt overrides or the embedded defaults.
func (c *Client) transcriptionInput(userPart responses.Part) []responses.Item {
	sys := strings.TrimSpace(c.system)
	if sys == "" {
		sys = defaultSystemPrompt
	}
	instructions := strings.TrimSpace(c.instr)
	if instructions == "" {
		instructions = defaultInstructions
	}
	return []responses.Item{
		{Role: responses.RoleSystem, Content: []responses.Part{responses.TextPart(sys)}},
		{Role: responses.RoleUser, Content: []responses.Part{
			responses.TextPart(instructions),
			userPart,
		}},
	}
}

// assistantItem wraps an assistant reply as an input item so it can be resent
// as prior context on follow-up calls.
func assistantItem(content string) responses.Item {
	return responses.Item{
		Role:    responses.RoleAssistant,
		Content: []responses.Part{responses.TextPart(content)},
	}
}

// newRequest assembles a responses.Request for the configured model, applying
// the optional temperature and the given max-output-tokens budget.
func (c *Client) newRequest(input []responses.Item, maxTokens *int) responses.Request {
	req := responses.Request{
		Model: c.model,
		Input: input,
	}
	if c.temperature != nil {
		req.Temperature = c.temperature
	}
	if maxTokens != nil {
		req.MaxOutputTokens = maxTokens
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
