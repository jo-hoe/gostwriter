package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/llm/openai/chat"
)

func TestOpenAI_TranscribeImage_Success(t *testing.T) {
	var seenAuth string
	var seenBody chat.Request

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&seenBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chat.Response{
			Choices: []chat.Choice{{Message: chat.ResponseMsg{Role: "assistant", Content: "Hello Markdown"}}},
		})
	}))
	defer ts.Close()

	cfg := config.OpenAISettings{
		BaseURL:      ts.URL,
		APIKey:       "k123",
		Model:        "gpt-5",
		SystemPrompt: "System X",
		Instructions: "User Instructions",
	}
	c := New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	out, _, err := c.TranscribeImage(ctx, bytes.NewBuffer([]byte("imgdata")), "image/png")
	if err != nil {
		t.Fatalf("TranscribeImage error: %v", err)
	}
	if out != "Hello Markdown" {
		t.Fatalf("unexpected content: %q", out)
	}
	if seenAuth != "Bearer k123" {
		t.Fatalf("missing/incorrect auth header, got %q", seenAuth)
	}
	if seenBody.Model != "gpt-5" {
		t.Fatalf("expected model gpt-5, got %q", seenBody.Model)
	}
	if len(seenBody.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(seenBody.Messages))
	}
	if seenBody.Messages[0].Role != chat.RoleSystem || seenBody.Messages[0].Content.(string) != "System X" {
		t.Fatalf("system prompt not set correctly: %+v", seenBody.Messages[0])
	}
	userParts, ok := seenBody.Messages[1].Content.([]any)
	if !ok || len(userParts) == 0 {
		t.Fatalf("user content not array of parts: %#v", seenBody.Messages[1].Content)
	}
	first, ok := userParts[0].(map[string]any)
	if !ok || first["type"] != "text" || first["text"] != "User Instructions" {
		t.Fatalf("first user part not text instructions: %#v", first)
	}
}

// stubServer returns an httptest server that always replies with the given
// completion content, decoding each request into *dst when dst is non-nil.
func stubServer(content string, dst *chat.Request) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dst != nil {
			_ = json.NewDecoder(r.Body).Decode(dst)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chat.Response{
			Choices: []chat.Choice{{Message: chat.ResponseMsg{Role: "assistant", Content: content}}},
		})
	}))
}

func captureUserParts(t *testing.T, mime string, data []byte) []any {
	t.Helper()
	var seenBody chat.Request
	ts := stubServer("ok", &seenBody)
	defer ts.Close()

	c := New(config.OpenAISettings{BaseURL: ts.URL, Model: "gpt-5"})
	if _, _, err := c.TranscribeImage(context.Background(), bytes.NewReader(data), mime); err != nil {
		t.Fatalf("TranscribeImage: %v", err)
	}
	parts, ok := seenBody.Messages[1].Content.([]any)
	if !ok {
		t.Fatalf("user content not array of parts: %#v", seenBody.Messages[1].Content)
	}
	return parts
}

func TestOpenAI_TranscribeImage_PNGUsesImageURLPart(t *testing.T) {
	parts := captureUserParts(t, "image/png", []byte("pngbytes"))
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts (text + image), got %d", len(parts))
	}
	img, ok := parts[1].(map[string]any)
	if !ok || img["type"] != "image_url" {
		t.Fatalf("second part should be image_url: %#v", parts[1])
	}
	urlObj, ok := img["image_url"].(map[string]any)
	if !ok {
		t.Fatalf("image_url object missing: %#v", img)
	}
	url, _ := urlObj["url"].(string)
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("image_url url prefix mismatch: %q", url)
	}
}

func TestOpenAI_TranscribeImage_PDFUsesFilePart(t *testing.T) {
	parts := captureUserParts(t, "application/pdf", []byte("%PDF-1.7"))
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts (text + file), got %d", len(parts))
	}
	fp, ok := parts[1].(map[string]any)
	if !ok || fp["type"] != "file" {
		t.Fatalf("second part should be file: %#v", parts[1])
	}
	fileObj, ok := fp["file"].(map[string]any)
	if !ok {
		t.Fatalf("file object missing: %#v", fp)
	}
	if fileObj["filename"] != "document.pdf" {
		t.Fatalf("filename mismatch: %#v", fileObj["filename"])
	}
	fileData, _ := fileObj["file_data"].(string)
	if !strings.HasPrefix(fileData, "data:application/pdf;base64,") {
		t.Fatalf("file_data prefix mismatch: %q", fileData)
	}
	if _, has := fp["image_url"]; has {
		t.Fatalf("pdf file part should not include image_url: %#v", fp)
	}
}

func TestOpenAI_TranscribeImage_Non200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer ts.Close()

	c := New(config.OpenAISettings{BaseURL: ts.URL, Model: "gpt-5"})
	_, _, err := c.TranscribeImage(context.Background(), bytes.NewBuffer([]byte("x")), "image/png")
	if err == nil {
		t.Fatalf("expected error for non-200 response")
	}
}

func TestOpenAI_TranscribeImage_EmptyImage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("server should not be called for empty image")
	}))
	defer ts.Close()

	c := New(config.OpenAISettings{BaseURL: ts.URL, Model: "gpt-5"})
	_, _, err := c.TranscribeImage(context.Background(), bytes.NewBuffer(nil), "image/png")
	if err == nil {
		t.Fatalf("expected error for empty image")
	}
}

func TestOpenAI_TranscribeImage_ReturnsConversation(t *testing.T) {
	ts := stubServer("transcribed", nil)
	defer ts.Close()

	c := New(config.OpenAISettings{BaseURL: ts.URL, Model: "gpt-5"})
	_, conv, err := c.TranscribeImage(context.Background(), bytes.NewReader([]byte("img")), "image/png")
	if err != nil {
		t.Fatalf("TranscribeImage: %v", err)
	}
	if conv == nil {
		t.Fatal("expected non-nil conversation")
	}
	if conv.Provider() != providerName {
		t.Fatalf("unexpected provider: %q", conv.Provider())
	}
}

func TestOpenAI_SuggestFilename_ContinuesConversation(t *testing.T) {
	var secondBody chat.Request
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 2 {
			_ = json.NewDecoder(r.Body).Decode(&secondBody)
		}
		content := "transcribed"
		if calls == 2 {
			content = `"invoice-november"`
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chat.Response{
			Choices: []chat.Choice{{Message: chat.ResponseMsg{Role: "assistant", Content: content}}},
		})
	}))
	defer ts.Close()

	c := New(config.OpenAISettings{BaseURL: ts.URL, Model: "gpt-5"})
	_, conv, err := c.TranscribeImage(context.Background(), bytes.NewReader([]byte("img")), "image/png")
	if err != nil {
		t.Fatalf("TranscribeImage: %v", err)
	}

	slug, err := c.SuggestFilename(context.Background(), conv)
	if err != nil {
		t.Fatalf("SuggestFilename: %v", err)
	}
	if slug != "invoice-november" {
		t.Fatalf("unexpected slug (quotes should be stripped): %q", slug)
	}
	// The follow-up call must reuse the transcription history: system + user
	// (instructions+document) + assistant reply + new filename instruction.
	if len(secondBody.Messages) != 4 {
		t.Fatalf("expected 4 messages continuing the conversation, got %d", len(secondBody.Messages))
	}
	if secondBody.Messages[2].Role != chat.RoleAssistant {
		t.Fatalf("expected assistant reply retained at index 2, got %q", secondBody.Messages[2].Role)
	}
	if secondBody.Messages[3].Role != chat.RoleUser {
		t.Fatalf("expected filename instruction as final user turn, got %q", secondBody.Messages[3].Role)
	}
}

func TestOpenAI_SuggestFilename_RejectsForeignConversation(t *testing.T) {
	c := New(config.OpenAISettings{BaseURL: "http://example.invalid", Model: "gpt-5"})
	_, err := c.SuggestFilename(context.Background(), foreignConversation{})
	if err == nil {
		t.Fatal("expected error for conversation from another provider")
	}
}

type foreignConversation struct{}

func (foreignConversation) Provider() string { return "other" }

func TestOpenAI_TranscribeImage_ContextCancel(t *testing.T) {
	var started int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.StoreInt32(&started, 1)
		time.Sleep(2 * time.Second)
	}))
	defer ts.Close()

	c := New(config.OpenAISettings{BaseURL: ts.URL, Model: "gpt-5"})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, _, err := c.TranscribeImage(ctx, bytes.NewBuffer([]byte("data")), "image/png")
	if err == nil {
		t.Fatalf("expected context cancellation error")
	}
	if atomic.LoadInt32(&started) == 0 {
		t.Fatalf("server was not invoked; test invalid")
	}
}
