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
	"github.com/jo-hoe/gostwriter/internal/llm/openai/responses"
)

// okResponse builds a Responses API reply carrying a single message output item
// with the given text.
func okResponse(content string) responses.Response {
	return responses.Response{Output: []responses.OutputItem{
		{Type: "message", Content: []responses.OutputContent{{Type: "output_text", Text: content}}},
	}}
}

func TestOpenAI_TranscribeImage_Success(t *testing.T) {
	var seenAuth string
	var seenBody responses.Request

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/responses" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&seenBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(okResponse("Hello Markdown"))
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
	if len(seenBody.Input) != 2 {
		t.Fatalf("expected 2 input items, got %d", len(seenBody.Input))
	}
	if seenBody.Input[0].Role != responses.RoleSystem {
		t.Fatalf("first item should be system, got %q", seenBody.Input[0].Role)
	}
	if len(seenBody.Input[0].Content) != 1 || seenBody.Input[0].Content[0].Text != "System X" {
		t.Fatalf("system prompt not set correctly: %+v", seenBody.Input[0])
	}
	if seenBody.Input[1].Role != responses.RoleUser || len(seenBody.Input[1].Content) == 0 {
		t.Fatalf("user item not populated: %+v", seenBody.Input[1])
	}
	first := seenBody.Input[1].Content[0]
	if first.Type != responses.PartInputText || first.Text != "User Instructions" {
		t.Fatalf("first user part not text instructions: %+v", first)
	}
}

// stubServer returns an httptest server that always replies with the given
// completion content, decoding each request into *dst when dst is non-nil.
func stubServer(content string, dst *responses.Request) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dst != nil {
			_ = json.NewDecoder(r.Body).Decode(dst)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(okResponse(content))
	}))
}

func captureUserParts(t *testing.T, mime string, data []byte) []responses.Part {
	t.Helper()
	var seenBody responses.Request
	ts := stubServer("ok", &seenBody)
	defer ts.Close()

	c := New(config.OpenAISettings{BaseURL: ts.URL, Model: "gpt-5"})
	if _, _, err := c.TranscribeImage(context.Background(), bytes.NewReader(data), mime); err != nil {
		t.Fatalf("TranscribeImage: %v", err)
	}
	if len(seenBody.Input) != 2 {
		t.Fatalf("expected 2 input items, got %d", len(seenBody.Input))
	}
	return seenBody.Input[1].Content
}

func TestOpenAI_TranscribeImage_PNGUsesInputImagePart(t *testing.T) {
	parts := captureUserParts(t, "image/png", []byte("pngbytes"))
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts (text + image), got %d", len(parts))
	}
	img := parts[1]
	if img.Type != responses.PartInputImage {
		t.Fatalf("second part should be input_image: %+v", img)
	}
	if !strings.HasPrefix(img.ImageURL, "data:image/png;base64,") {
		t.Fatalf("image_url prefix mismatch: %q", img.ImageURL)
	}
}

func TestOpenAI_TranscribeImage_PDFUsesInputFilePart(t *testing.T) {
	// The Responses API supports PDFs natively; the provider must send an
	// "input_file" part carrying the inline-base64 document.
	parts := captureUserParts(t, "application/pdf", []byte("%PDF-1.7"))
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts (text + file), got %d", len(parts))
	}
	file := parts[1]
	if file.Type != responses.PartInputFile {
		t.Fatalf("second part should be input_file: %+v", file)
	}
	if !strings.HasPrefix(file.FileData, "data:application/pdf;base64,") {
		t.Fatalf("file_data prefix mismatch: %q", file.FileData)
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
	var secondBody responses.Request
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
		_ = json.NewEncoder(w).Encode(okResponse(content))
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
	if len(secondBody.Input) != 4 {
		t.Fatalf("expected 4 input items continuing the conversation, got %d", len(secondBody.Input))
	}
	if secondBody.Input[2].Role != responses.RoleAssistant {
		t.Fatalf("expected assistant reply retained at index 2, got %q", secondBody.Input[2].Role)
	}
	if secondBody.Input[3].Role != responses.RoleUser {
		t.Fatalf("expected filename instruction as final user turn, got %q", secondBody.Input[3].Role)
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
