package aiproxy

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
)

func TestAIProxy_TranscribeImage_Success(t *testing.T) {
	var seenAuth string
	var seenBody chatCompletionRequest

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&seenBody); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := chatCompletionResponse{
			ID:      "id-123",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Choices: []chatCompletionChoice{
				{
					Index: 0,
					Message: responseMsg{
						Role:    "assistant",
						Content: "Hello Markdown",
					},
					FinishReason: "stop",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	cfg := config.AIProxySettings{
		BaseURL:      ts.URL,
		APIKey:       "k123",
		Model:        "gpt-5",
		SystemPrompt: "System X",
		Instructions: "User Instructions",
	}
	c := New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	out, err := c.TranscribeImage(ctx, bytes.NewBuffer([]byte("imgdata")), "image/png")
	if err != nil {
		t.Fatalf("TranscribeImage error: %v", err)
	}
	if out != "Hello Markdown" {
		t.Fatalf("unexpected content: %q", out)
	}
	if seenAuth != "Bearer k123" {
		t.Fatalf("missing/incorrect auth header, got %q", seenAuth)
	}
	// Validate the request carried our prompts and model
	if seenBody.Model != "gpt-5" {
		t.Fatalf("expected model gpt-5, got %q", seenBody.Model)
	}
	if len(seenBody.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(seenBody.Messages))
	}
	if seenBody.Messages[0].Role != "system" || seenBody.Messages[0].Content.(string) != "System X" {
		t.Fatalf("system prompt not set correctly: %+v", seenBody.Messages[0])
	}
	// user content is []messagePart (marshalled as []any). Check first part is text with our instructions.
	userParts, ok := seenBody.Messages[1].Content.([]any)
	if !ok || len(userParts) == 0 {
		t.Fatalf("user content not array of parts: %#v", seenBody.Messages[1].Content)
	}
	first, ok := userParts[0].(map[string]any)
	if !ok || first["type"] != "text" || first["text"] != "User Instructions" {
		t.Fatalf("first user part not text instructions: %#v", first)
	}
}

// captureUserParts runs TranscribeImage against a stub server and returns the
// user message content parts (each a map[string]any) as decoded from the request.
func captureUserParts(t *testing.T, mime string, data []byte) []any {
	t.Helper()
	var seenBody chatCompletionRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seenBody); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chatCompletionResponse{
			Choices: []chatCompletionChoice{{Message: responseMsg{Content: "ok"}}},
		})
	}))
	defer ts.Close()

	c := New(config.AIProxySettings{BaseURL: ts.URL, Model: "gpt-5"})
	if _, err := c.TranscribeImage(context.Background(), bytes.NewReader(data), mime); err != nil {
		t.Fatalf("TranscribeImage: %v", err)
	}
	parts, ok := seenBody.Messages[1].Content.([]any)
	if !ok {
		t.Fatalf("user content not array of parts: %#v", seenBody.Messages[1].Content)
	}
	return parts
}

func TestAIProxy_TranscribeImage_PNGUsesImageURLPart(t *testing.T) {
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

func TestAIProxy_TranscribeImage_PDFUsesFilePart(t *testing.T) {
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
	// A PDF must not also carry an image_url part.
	if _, has := fp["image_url"]; has {
		t.Fatalf("pdf file part should not include image_url: %#v", fp)
	}
}

func TestAIProxy_TranscribeImage_Non200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer ts.Close()

	cfg := config.AIProxySettings{
		BaseURL: ts.URL,
		Model:   "gpt-5",
	}
	c := New(cfg)

	_, err := c.TranscribeImage(context.Background(), bytes.NewBuffer([]byte("x")), "image/png")
	if err == nil {
		t.Fatalf("expected error for non-200 response")
	}
}

func TestAIProxy_TranscribeImage_EmptyImage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("server should not be called for empty image")
	}))
	defer ts.Close()

	cfg := config.AIProxySettings{
		BaseURL: ts.URL,
		Model:   "gpt-5",
	}
	c := New(cfg)

	_, err := c.TranscribeImage(context.Background(), bytes.NewBuffer(nil), "image/png")
	if err == nil {
		t.Fatalf("expected error for empty image")
	}
}

func TestAIProxy_GenerateTitle_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body chatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		// Title request must be text-only (string content, not []messagePart)
		if _, ok := body.Messages[1].Content.(string); !ok {
			http.Error(w, "expected string content", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chatCompletionResponse{
			Choices: []chatCompletionChoice{
				{Message: responseMsg{Content: "My Generated Title"}},
			},
		})
	}))
	defer ts.Close()

	c := New(config.AIProxySettings{BaseURL: ts.URL, Model: "gpt-5"})
	title, err := c.GenerateTitle(context.Background(), "# Some markdown content")
	if err != nil {
		t.Fatal(err)
	}
	if title != "My Generated Title" {
		t.Fatalf("unexpected title: %q", title)
	}
}

func TestAIProxy_GenerateTitle_StripsSurroundingQuotes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chatCompletionResponse{
			Choices: []chatCompletionChoice{
				{Message: responseMsg{Content: `"Quoted Title"`}},
			},
		})
	}))
	defer ts.Close()

	c := New(config.AIProxySettings{BaseURL: ts.URL, Model: "gpt-5"})
	title, err := c.GenerateTitle(context.Background(), "some markdown")
	if err != nil {
		t.Fatal(err)
	}
	if title != "Quoted Title" {
		t.Fatalf("quotes not stripped: %q", title)
	}
}

func TestAIProxy_GenerateTitle_EmptyCompletion(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chatCompletionResponse{Choices: []chatCompletionChoice{}})
	}))
	defer ts.Close()

	c := New(config.AIProxySettings{BaseURL: ts.URL, Model: "gpt-5"})
	_, err := c.GenerateTitle(context.Background(), "some markdown")
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}

func TestAIProxy_GenerateTitle_Non200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := New(config.AIProxySettings{BaseURL: ts.URL, Model: "gpt-5"})
	_, err := c.GenerateTitle(context.Background(), "some markdown")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestAIProxy_TranscribeImage_ContextCancel(t *testing.T) {
	var started int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.StoreInt32(&started, 1)
		time.Sleep(2 * time.Second)
	}))
	defer ts.Close()

	cfg := config.AIProxySettings{
		BaseURL: ts.URL,
		Model:   "gpt-5",
	}
	c := New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := c.TranscribeImage(ctx, bytes.NewBuffer([]byte("data")), "image/png")
	if err == nil {
		t.Fatalf("expected context cancellation error")
	}
	if atomic.LoadInt32(&started) == 0 {
		t.Fatalf("server was not invoked; test invalid")
	}
}
