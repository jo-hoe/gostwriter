// Package messages implements the Anthropic Messages API wire protocol and a
// thin HTTP transport for it. It is endpoint-agnostic: any gateway that speaks
// the native Anthropic Messages API (api.anthropic.com or a reverse proxy
// exposing an /anthropic route) can be driven through it.
package messages

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jo-hoe/gostwriter/internal/common"
)

const (
	headerContentType      = "Content-Type"
	headerAPIKey           = "x-api-key"
	headerAnthropicVersion = "anthropic-version"

	// anthropicVersion is the required API version header value.
	anthropicVersion = "2023-06-01"

	endpointMessages = "v1/messages"

	defaultHTTPTimeout = 5 * time.Minute
	errorSnippetLimit  = 400

	// defaultMaxTokens is used when the caller does not set a token budget. The
	// Anthropic Messages API requires max_tokens on every request.
	defaultMaxTokens = 4096

	sourceTypeBase64 = "base64"

	// pdfMediaType is the media_type sent for PDF "document" blocks.
	pdfMediaType = common.MimeApplicationPDF
)

// Role is the sender role for a message.
type Role string

// Role constants for message senders.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// BlockType is the type discriminator for a content block.
type BlockType string

// BlockType constants for content blocks.
const (
	BlockText     BlockType = "text"
	BlockImage    BlockType = "image"
	BlockDocument BlockType = "document"
)

// Request is an Anthropic Messages API request. MaxTokens is required by the
// API and is always serialized.
type Request struct {
	Model       string    `json:"model"`
	MaxTokens   int       `json:"max_tokens"`
	System      string    `json:"system,omitempty"`
	Messages    []Message `json:"messages"`
	Temperature *float32  `json:"temperature,omitempty"`
}

// Message is a single message in the conversation. Content is a list of blocks.
type Message struct {
	Role    Role    `json:"role"`
	Content []Block `json:"content"`
}

// Block is a single content block of a message. Exactly one of Text or Source
// is populated depending on Type.
type Block struct {
	Type   BlockType `json:"type"`
	Text   *string   `json:"text,omitempty"`
	Source *Source   `json:"source,omitempty"`
}

// Source is the payload for an "image" or "document" block carrying inline
// base64 data together with its media type.
type Source struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// Response is an Anthropic Messages API response.
type Response struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	Content    []ResponseItem `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      *Usage         `json:"usage,omitempty"`
}

// ResponseItem is a single content item carried by a response.
type ResponseItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Usage reports token accounting for a response.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Client is a thin transport over the Anthropic Messages endpoint. It is safe
// for concurrent use.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

// NewClient creates a messages transport pointed at baseURL. apiKey is optional;
// when set it is sent as the x-api-key header. A zero timeout falls back to a 5m
// default.
func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = defaultHTTPTimeout
	}
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
	}
}

// Complete executes a messages request and returns the concatenated text of the
// response content blocks. It returns an error for transport failures, non-2xx
// responses, and empty completions.
func (c *Client) Complete(ctx context.Context, req Request) (string, error) {
	if req.MaxTokens <= 0 {
		req.MaxTokens = defaultMaxTokens
	}

	u, err := url.JoinPath(c.baseURL, endpointMessages)
	if err != nil {
		return "", fmt.Errorf("join url: %w", err)
	}

	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set(headerContentType, common.ContentTypeJSON)
	httpReq.Header.Set(headerAnthropicVersion, anthropicVersion)
	if strings.TrimSpace(c.apiKey) != "" {
		httpReq.Header.Set(headerAPIKey, c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("messages: %w", ctx.Err())
		}
		return "", fmt.Errorf("http do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("messages status %d: %s", resp.StatusCode, truncate(string(respBytes), errorSnippetLimit))
	}

	var out Response
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	text := joinTextBlocks(out.Content)
	if text == "" {
		return "", fmt.Errorf("empty completion")
	}
	return text, nil
}

// BuildDocumentBlock returns the user content block for a document, selecting the
// "document" block for PDFs and the "image" block for everything else. The data
// is carried as raw base64 (not a data URL) with the media type kept separate,
// as the Anthropic Messages API expects.
func BuildDocumentBlock(mime string, data []byte) Block {
	enc := base64.StdEncoding.EncodeToString(data)
	mt := strings.TrimSpace(mime)
	if strings.EqualFold(mt, common.MimeApplicationPDF) {
		return Block{
			Type:   BlockDocument,
			Source: &Source{Type: sourceTypeBase64, MediaType: pdfMediaType, Data: enc},
		}
	}
	return Block{
		Type:   BlockImage,
		Source: &Source{Type: sourceTypeBase64, MediaType: mt, Data: enc},
	}
}

// TextBlock returns a text content block.
func TextBlock(text string) Block {
	return Block{Type: BlockText, Text: &text}
}

// joinTextBlocks concatenates the text of all text-bearing response items.
func joinTextBlocks(items []ResponseItem) string {
	var b strings.Builder
	for _, it := range items {
		if it.Text != "" {
			b.WriteString(it.Text)
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
