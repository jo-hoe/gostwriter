// Package responses implements the OpenAI-compatible Responses API wire protocol
// and a thin HTTP transport for it. It is provider-agnostic: any endpoint that
// speaks the OpenAI Responses API (OpenAI itself, a reverse proxy, or a
// compatible gateway) can be driven through it. Unlike the chat-completions
// route, the Responses API accepts inline-base64 PDFs via "input_file" parts in
// addition to images via "input_image" parts.
package responses

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

	endpointResponses = "v1/responses"

	defaultHTTPTimeout = 5 * time.Minute
	errorSnippetLimit  = 400

	dataURLPrefix    = "data:"
	dataURLBase64Sep = ";base64,"

	// pdfPartFilename is the filename sent for PDF "input_file" content parts.
	pdfPartFilename = "document.pdf"
)

// Role is the sender role for an input item.
type Role string

// Role constants for input item senders.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// PartType is the type discriminator for a multimodal input part.
type PartType string

// PartType constants for multimodal input parts.
const (
	PartInputText  PartType = "input_text"
	PartInputImage PartType = "input_image"
	PartInputFile  PartType = "input_file"
)

// Output item/content type discriminators returned by the API.
const (
	outputTypeMessage    = "message"
	outputTypeOutputText = "output_text"
)

// Request is an OpenAI-compatible Responses API request.
type Request struct {
	Model           string   `json:"model"`
	Input           []Item   `json:"input"`
	Temperature     *float32 `json:"temperature,omitempty"`
	MaxOutputTokens *int     `json:"max_output_tokens,omitempty"`
}

// Item is a single input item (one conversational turn).
type Item struct {
	Role    Role   `json:"role"`
	Content []Part `json:"content"`
}

// Part is a single multimodal content part of an input item. The "input_image"
// part carries a plain data-URL string, while "input_file" carries a filename
// and the data URL in file_data.
type Part struct {
	Type     PartType `json:"type"`
	Text     string   `json:"text,omitempty"`
	ImageURL string   `json:"image_url,omitempty"`
	Filename string   `json:"filename,omitempty"`
	FileData string   `json:"file_data,omitempty"`
}

// Response is an OpenAI-compatible Responses API response.
type Response struct {
	ID     string       `json:"id"`
	Object string       `json:"object"`
	Output []OutputItem `json:"output"`
	Usage  *Usage       `json:"usage,omitempty"`
}

// OutputItem is a single item in the output array. Only items with type
// "message" carry assistant text; others (e.g. "reasoning") are skipped.
type OutputItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content []OutputContent `json:"content"`
}

// OutputContent is a single content block within a message output item.
type OutputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Usage reports token accounting for a response.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// Client is a thin transport over the OpenAI-compatible Responses endpoint. It
// is safe for concurrent use.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

// NewClient creates a Responses transport pointed at baseURL. apiKey is
// optional; when set it is sent as a Bearer token. A zero timeout falls back to
// a 5m default.
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

// Complete executes a Responses API request and returns the concatenated
// assistant output text. It returns an error for transport failures, non-2xx
// responses, and empty output.
func (c *Client) Complete(ctx context.Context, req Request) (string, error) {
	u, err := url.JoinPath(c.baseURL, endpointResponses)
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
			return "", fmt.Errorf("responses: %w", ctx.Err())
		}
		return "", fmt.Errorf("http do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("responses status %d: %s", resp.StatusCode, truncate(string(respBytes), errorSnippetLimit))
	}

	var out Response
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	text := extractOutputText(out.Output)
	if text == "" {
		return "", fmt.Errorf("empty completion")
	}
	return text, nil
}

// extractOutputText concatenates the text of every "output_text" content block
// across "message" output items, skipping non-message items such as
// "reasoning".
func extractOutputText(items []OutputItem) string {
	var b strings.Builder
	for _, item := range items {
		if item.Type != outputTypeMessage {
			continue
		}
		for _, cp := range item.Content {
			if cp.Type == outputTypeOutputText {
				b.WriteString(cp.Text)
			}
		}
	}
	return b.String()
}

// BuildDocumentPart returns the input content part for a document, selecting the
// "input_file" part for PDFs and the "input_image" part for everything else.
func BuildDocumentPart(mime string, data []byte) Part {
	dataURL := BuildDataURL(mime, data)
	if strings.EqualFold(strings.TrimSpace(mime), common.MimeApplicationPDF) {
		return Part{
			Type:     PartInputFile,
			Filename: pdfPartFilename,
			FileData: dataURL,
		}
	}
	return Part{
		Type:     PartInputImage,
		ImageURL: dataURL,
	}
}

// TextPart returns an "input_text" content part carrying s.
func TextPart(s string) Part {
	return Part{Type: PartInputText, Text: s}
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
