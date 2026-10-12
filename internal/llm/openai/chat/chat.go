// Package chat implements the OpenAI-compatible Chat Completions wire protocol
// and a thin HTTP transport for it. It is provider-agnostic: any endpoint that
// speaks the OpenAI chat-completions API (OpenAI itself, an ai-proxy reverse
// proxy, or a compatible gateway) can be driven through it.
package chat

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
	headerContentType   = "Content-Type"
	headerAuthorization = "Authorization"

	contentTypeOctetStream = "application/octet-stream"

	authSchemeBearer = "Bearer"

	endpointChatCompletions = "v1/chat/completions"

	defaultHTTPTimeout = 5 * time.Minute
	errorSnippetLimit  = 400

	dataURLPrefix    = "data:"
	dataURLBase64Sep = ";base64,"

	// pdfPartFilename is the filename sent for PDF "file" content parts.
	pdfPartFilename = "document.pdf"
)

// Role is the sender role for a chat message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// PartType is the type discriminator for a multimodal message part.
type PartType string

const (
	PartText     PartType = "text"
	PartImageURL PartType = "image_url"
	PartFile     PartType = "file"
)

// Request is an OpenAI-compatible chat completion request.
type Request struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature *float32  `json:"temperature,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
	Tools       any       `json:"tools,omitempty"`
	ResponseFmt any       `json:"response_format,omitempty"`
}

// Message is a single chat message. Content is either a string or a []Part.
type Message struct {
	Role    Role   `json:"role"`
	Content any    `json:"content"`
	Name    string `json:"name,omitempty"`
}

// Part is a single multimodal content part of a user message.
type Part struct {
	Type     PartType  `json:"type"`
	Text     *string   `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
	File     *FilePart `json:"file,omitempty"`
}

// ImageURL is the payload for an "image_url" part.
type ImageURL struct {
	URL    string  `json:"url"`
	Detail *string `json:"detail,omitempty"`
}

// FilePart is the payload for a "file" part (e.g. a PDF).
type FilePart struct {
	Filename string `json:"filename"`
	FileData string `json:"file_data"`
}

// Response is an OpenAI-compatible chat completion response.
type Response struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Choice is a single completion choice.
type Choice struct {
	Index        int         `json:"index"`
	Message      ResponseMsg `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// ResponseMsg is the assistant message carried by a completion choice.
type ResponseMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Usage reports token accounting for a completion.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Client is a thin transport over the OpenAI-compatible chat completions
// endpoint. It is safe for concurrent use.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

// NewClient creates a chat transport pointed at baseURL. apiKey is optional; when
// set it is sent as a Bearer token. A zero timeout falls back to a 5m default.
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

// Complete executes a chat completion request and returns the first choice's
// content. It returns an error for transport failures, non-2xx responses, and
// empty completions.
func (c *Client) Complete(ctx context.Context, req Request) (string, error) {
	u, err := url.JoinPath(c.baseURL, endpointChatCompletions)
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
	if strings.TrimSpace(c.apiKey) != "" {
		httpReq.Header.Set(headerAuthorization, authSchemeBearer+" "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("http do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("chat status %d: %s", resp.StatusCode, truncate(string(respBytes), errorSnippetLimit))
	}

	var comp Response
	if err := json.Unmarshal(respBytes, &comp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if len(comp.Choices) == 0 || comp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("empty completion")
	}
	return comp.Choices[0].Message.Content, nil
}

// BuildDocumentPart returns the user content part for a document, selecting the
// "file" part for PDFs and the "image_url" part for everything else.
func BuildDocumentPart(mime string, data []byte) Part {
	dataURL := BuildDataURL(mime, data)
	if strings.EqualFold(strings.TrimSpace(mime), common.MimeApplicationPDF) {
		return Part{
			Type: PartFile,
			File: &FilePart{Filename: pdfPartFilename, FileData: dataURL},
		}
	}
	return Part{
		Type:     PartImageURL,
		ImageURL: &ImageURL{URL: dataURL},
	}
}

// BuildDataURL encodes data as a base64 data URL with the given mime type,
// defaulting to application/octet-stream when mime is blank.
func BuildDataURL(mime string, data []byte) string {
	mt := strings.TrimSpace(mime)
	if mt == "" {
		mt = contentTypeOctetStream
	}
	enc := base64.StdEncoding.EncodeToString(data)
	return dataURLPrefix + mt + dataURLBase64Sep + enc
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
