package aiproxy

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
	"github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/llm"
)

var _ llm.Client = (*Client)(nil)

const (
	// Headers
	headerContentType   = "Content-Type"
	headerAuthorization = "Authorization"

	// Content types
	contentTypeOctetStream = "application/octet-stream"

	// Auth
	authSchemeBearer = "Bearer"

	// Endpoints
	endpointChatCompletions = "v1/chat/completions"

	// Timeouts and limits
	defaultTimeout    = 60 * time.Second
	errorSnippetLimit = 400

	// Defaults
	defaultSystemPrompt = "You are an expert OCR and document understanding assistant. Transcribe the provided image into clean, readable Markdown. Preserve headings, lists, tables, code blocks, and semantic structure. Do not add commentary; output only the transcription."
	defaultInstructions = "Please transcribe the content of this image into Markdown. Keep the original structure and formatting."

	// Data URL constants
	dataURLPrefix    = "data:"
	dataURLBase64Sep = ";base64,"
)

// Role represents the sender role for a chat message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// PartType represents the type for a multimodal message part.
type PartType string

const (
	PartText     PartType = "text"
	PartImageURL PartType = "image_url"
)

// Client implements llm.Client by calling an OpenAI-compatible AI Proxy.
type Client struct {
	httpClient  *http.Client
	baseURL     string
	apiKey      string
	model       string
	system      string
	instr       string
	temperature *float32
	maxTokens   *int
}

// New creates a new AI Proxy LLM client.
func New(cfg config.AIProxySettings) *Client {
	return &Client{
		httpClient:  newHTTPClient(),
		baseURL:     strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:      cfg.APIKey,
		model:       cfg.Model,
		system:      cfg.SystemPrompt,
		instr:       cfg.Instructions,
		temperature: optionalFloat32(cfg.Temperature),
		maxTokens:   optionalInt(cfg.MaxTokens),
	}
}

func newHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultTimeout}
}

// TranscribeImage sends a chat completion request instructing the model to transcribe the image into Markdown.
func (c *Client) TranscribeImage(ctx context.Context, r io.Reader, mime string) (string, error) {
	imgData, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read image: %w", err)
	}
	if len(imgData) == 0 {
		return "", fmt.Errorf("image is empty")
	}

	dataURL := buildDataURL(mime, imgData)
	reqBody := c.buildRequestBody(dataURL)

	u, err := url.JoinPath(c.baseURL, endpointChatCompletions)
	if err != nil {
		return "", fmt.Errorf("join url: %w", err)
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	req.Header.Set(headerContentType, common.ContentTypeJSON)
	if strings.TrimSpace(c.apiKey) != "" {
		req.Header.Set(headerAuthorization, authSchemeBearer+" "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("http do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("aiproxy status %d: %s", resp.StatusCode, truncate(string(respBytes), errorSnippetLimit))
	}

	var comp chatCompletionResponse
	if err := json.Unmarshal(respBytes, &comp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if len(comp.Choices) == 0 || comp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("empty completion")
	}
	return comp.Choices[0].Message.Content, nil
}

func (c *Client) buildRequestBody(imageDataURL string) chatCompletionRequest {
	sys := strings.TrimSpace(c.system)
	if sys == "" {
		sys = defaultSystemPrompt
	}
	instructions := strings.TrimSpace(c.instr)
	if instructions == "" {
		instructions = defaultInstructions
	}

	msgs := []chatMessage{
		{
			Role:    RoleSystem,
			Content: sys,
		},
		{
			Role: RoleUser,
			Content: []messagePart{
				{Type: PartText, Text: &instructions},
				{Type: PartImageURL, ImageURL: &imageURL{URL: imageDataURL}},
			},
		},
	}

	req := chatCompletionRequest{
		Model:    c.model,
		Messages: msgs,
		Stream:   false,
	}
	if c.temperature != nil {
		req.Temperature = c.temperature
	}
	if c.maxTokens != nil {
		req.MaxTokens = c.maxTokens
	}
	return req
}

func buildDataURL(mime string, data []byte) string {
	mt := strings.TrimSpace(mime)
	if mt == "" {
		mt = contentTypeOctetStream
	}
	enc := base64.StdEncoding.EncodeToString(data)
	return dataURLPrefix + mt + dataURLBase64Sep + enc
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// OpenAI-compatible Chat Completions request/response types

type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature *float32      `json:"temperature,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	Tools       any           `json:"tools,omitempty"`
	ResponseFmt any           `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    Role   `json:"role"`
	Content any    `json:"content"` // string or []messagePart
	Name    string `json:"name,omitempty"`
}

type messagePart struct {
	Type     PartType  `json:"type"`                // "text" | "image_url"
	Text     *string   `json:"text,omitempty"`      // when Type == "text"
	ImageURL *imageURL `json:"image_url,omitempty"` // when Type == "image_url"
}

type imageURL struct {
	URL    string  `json:"url"`
	Detail *string `json:"detail,omitempty"`
}

type chatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Choices []chatCompletionChoice `json:"choices"`
	Usage   *chatCompletionUsage   `json:"usage,omitempty"`
}

type chatCompletionChoice struct {
	Index        int         `json:"index"`
	Message      responseMsg `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type responseMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
