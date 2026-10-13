package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/llm/anthropic/messages"
)

// stubServer replies with the given completion text, decoding each request into
// *dst when dst is non-nil.
func stubServer(text string, dst *messages.Request) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dst != nil {
			_ = json.NewDecoder(r.Body).Decode(dst)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(messages.Response{
			Content: []messages.ResponseItem{{Type: "text", Text: text}},
		})
	}))
}

// captureUserBlocks runs a transcription and returns the blocks of the user
// message as decoded generically from the wire.
func captureUserBlocks(t *testing.T, mime string, data []byte) []any {
	t.Helper()
	var seen struct {
		System   string `json:"system"`
		Messages []struct {
			Role    string `json:"role"`
			Content []any  `json:"content"`
		} `json:"messages"`
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seen)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(messages.Response{Content: []messages.ResponseItem{{Type: "text", Text: "ok"}}})
	}))
	defer ts.Close()

	c := New(config.AnthropicSettings{BaseURL: ts.URL, Model: "claude", MaxTokens: 1024})
	if _, _, err := c.TranscribeImage(context.Background(), bytes.NewReader(data), mime); err != nil {
		t.Fatalf("TranscribeImage: %v", err)
	}
	if len(seen.Messages) != 1 || seen.Messages[0].Role != "user" {
		t.Fatalf("expected one user message, got %+v", seen.Messages)
	}
	if seen.System == "" {
		t.Fatalf("expected a system prompt on the request")
	}
	return seen.Messages[0].Content
}

func TestAnthropic_TranscribeImage_PNGUsesImageBlock(t *testing.T) {
	blocks := captureUserBlocks(t, "image/png", []byte("pngbytes"))
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks (text + image), got %d", len(blocks))
	}
	img, ok := blocks[1].(map[string]any)
	if !ok || img["type"] != "image" {
		t.Fatalf("second block should be image: %#v", blocks[1])
	}
	src, ok := img["source"].(map[string]any)
	if !ok || src["media_type"] != "image/png" || src["type"] != "base64" {
		t.Fatalf("image source malformed: %#v", img)
	}
	if src["data"] == "" {
		t.Fatalf("image source data missing: %#v", src)
	}
}

func TestAnthropic_TranscribeImage_PDFUsesDocumentBlock(t *testing.T) {
	blocks := captureUserBlocks(t, "application/pdf", []byte("%PDF-1.7"))
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks (text + document), got %d", len(blocks))
	}
	doc, ok := blocks[1].(map[string]any)
	if !ok || doc["type"] != "document" {
		t.Fatalf("second block should be document: %#v", blocks[1])
	}
	src, ok := doc["source"].(map[string]any)
	if !ok || src["media_type"] != "application/pdf" {
		t.Fatalf("document source malformed: %#v", doc)
	}
}

func TestAnthropic_TranscribeImage_EmptyDocument(t *testing.T) {
	c := New(config.AnthropicSettings{BaseURL: "http://example.invalid", Model: "claude"})
	if _, _, err := c.TranscribeImage(context.Background(), bytes.NewReader(nil), "application/pdf"); err == nil {
		t.Fatal("expected error for empty document")
	}
}

func TestAnthropic_SuggestFilename_ContinuesConversation(t *testing.T) {
	var lastReq messages.Request
	ts := stubServer("my-doc-slug", &lastReq)
	defer ts.Close()

	c := New(config.AnthropicSettings{BaseURL: ts.URL, Model: "claude", MaxTokens: 1024})
	_, conv, err := c.TranscribeImage(context.Background(), bytes.NewReader([]byte("x")), "image/png")
	if err != nil {
		t.Fatalf("TranscribeImage: %v", err)
	}

	slug, err := c.SuggestFilename(context.Background(), conv)
	if err != nil {
		t.Fatalf("SuggestFilename: %v", err)
	}
	if slug != "my-doc-slug" {
		t.Fatalf("unexpected slug: %q", slug)
	}
	// The follow-up request should carry the prior exchange (user doc + assistant
	// reply) plus the new filename instruction = at least 3 messages.
	if len(lastReq.Messages) < 3 {
		t.Fatalf("expected conversation to be continued, got %d messages", len(lastReq.Messages))
	}
}

func TestAnthropic_SuggestFilename_WrongConversationType(t *testing.T) {
	c := New(config.AnthropicSettings{BaseURL: "http://example.invalid", Model: "claude"})
	if _, err := c.SuggestFilename(context.Background(), fakeConv{}); err == nil {
		t.Fatal("expected error for foreign conversation type")
	}
}

type fakeConv struct{}

func (fakeConv) Provider() string { return "other" }

func TestAnthropic_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(time.Second)
	}))
	defer ts.Close()

	c := New(config.AnthropicSettings{BaseURL: ts.URL, Model: "claude", Timeout: 20 * time.Millisecond})
	if _, _, err := c.TranscribeImage(context.Background(), bytes.NewReader([]byte("x")), "image/png"); err == nil {
		t.Fatal("expected timeout error")
	}
}
