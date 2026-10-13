package messages

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBuildDocumentBlock_ImageIsImageBlock(t *testing.T) {
	b := BuildDocumentBlock("image/png", []byte("hi"))
	if b.Type != BlockImage || b.Source == nil || b.Text != nil {
		t.Fatalf("expected image block, got %+v", b)
	}
	if b.Source.Type != sourceTypeBase64 || b.Source.MediaType != "image/png" {
		t.Fatalf("unexpected source: %+v", b.Source)
	}
	// "hi" base64 is "aGk=" and must be raw base64, not a data URL.
	if b.Source.Data != "aGk=" {
		t.Fatalf("expected raw base64 data, got %q", b.Source.Data)
	}
}

func TestBuildDocumentBlock_PDFIsDocumentBlock(t *testing.T) {
	b := BuildDocumentBlock("application/pdf", []byte("%PDF"))
	if b.Type != BlockDocument || b.Source == nil {
		t.Fatalf("expected document block, got %+v", b)
	}
	if b.Source.MediaType != "application/pdf" {
		t.Fatalf("unexpected media type: %q", b.Source.MediaType)
	}
}

// TestClient_Complete_PDFDocumentBlockOnTheWire pins the exact wire shape of a
// PDF document block. The Anthropic Messages API accepts PDFs natively via a
// "document" block whose source carries raw base64 and a separate media_type;
// this guards against a refactor silently dropping or reshaping that nested
// source object.
func TestClient_Complete_PDFDocumentBlockOnTheWire(t *testing.T) {
	var raw []byte
	var version string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version = r.Header.Get("anthropic-version")
		raw, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{Content: []ResponseItem{{Type: "text", Text: "ok"}}})
	}))
	defer ts.Close()

	block := BuildDocumentBlock("application/pdf", []byte("%PDF-1.4 regression"))
	c := NewClient(ts.URL, "key", 0)
	_, err := c.Complete(context.Background(), Request{
		Model:    "claude",
		Messages: []Message{{Role: RoleUser, Content: []Block{TextBlock("transcribe"), block}}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if version != anthropicVersion {
		t.Fatalf("anthropic-version header mismatch: %q", version)
	}

	// Decode generically to inspect what actually crossed the wire.
	var sent struct {
		MaxTokens int `json:"max_tokens"`
		Messages  []struct {
			Content []struct {
				Type   string `json:"type"`
				Source *struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatalf("unmarshal sent body: %v (raw=%s)", err, raw)
	}
	if sent.MaxTokens <= 0 {
		t.Fatalf("max_tokens must be set on the wire, got %d (raw=%s)", sent.MaxTokens, raw)
	}
	if len(sent.Messages) != 1 || len(sent.Messages[0].Content) != 2 {
		t.Fatalf("unexpected message/content shape: %s", raw)
	}
	doc := sent.Messages[0].Content[1]
	if doc.Type != string(BlockDocument) {
		t.Fatalf("expected document block, got %q", doc.Type)
	}
	if doc.Source == nil {
		t.Fatalf("nested source object missing on the wire: %s", raw)
	}
	if doc.Source.Type != sourceTypeBase64 || doc.Source.MediaType != "application/pdf" || doc.Source.Data == "" {
		t.Fatalf("document source malformed on the wire: %+v", doc.Source)
	}
}

func TestClient_Complete_Success(t *testing.T) {
	var apiKey string
	var seen Request
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey = r.Header.Get("x-api-key")
		if r.URL.Path != "/v1/messages" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&seen)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{Content: []ResponseItem{
			{Type: "text", Text: "Hello "},
			{Type: "text", Text: "world"},
		}})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "tok", 0)
	out, err := c.Complete(context.Background(), Request{
		Model:    "claude",
		Messages: []Message{{Role: RoleUser, Content: []Block{TextBlock("hi")}}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "Hello world" {
		t.Fatalf("expected concatenated text, got %q", out)
	}
	if apiKey != "tok" {
		t.Fatalf("x-api-key mismatch: %q", apiKey)
	}
	if seen.Model != "claude" {
		t.Fatalf("model not forwarded: %q", seen.Model)
	}
}

func TestClient_Complete_OmitsKeyWhenEmpty(t *testing.T) {
	var apiKey string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{Content: []ResponseItem{{Type: "text", Text: "ok"}}})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "", 0)
	if _, err := c.Complete(context.Background(), Request{Model: "m"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if apiKey != "" {
		t.Fatalf("expected no x-api-key header, got %q", apiKey)
	}
}

func TestClient_Complete_Non200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadRequest)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "", 0)
	if _, err := c.Complete(context.Background(), Request{Model: "m"}); err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestClient_Complete_EmptyContent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{Content: []ResponseItem{}})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "", 0)
	if _, err := c.Complete(context.Background(), Request{Model: "m"}); err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestClient_Complete_ContextCancel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "", 0)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Complete(ctx, Request{Model: "m"}); err == nil {
		t.Fatal("expected context cancellation error")
	}
}
