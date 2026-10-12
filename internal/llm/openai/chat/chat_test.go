package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBuildDataURL_DefaultsMime(t *testing.T) {
	got := BuildDataURL("", []byte("abc"))
	if !strings.HasPrefix(got, "data:application/octet-stream;base64,") {
		t.Fatalf("expected octet-stream default, got %q", got)
	}
}

func TestBuildDataURL_EncodesData(t *testing.T) {
	got := BuildDataURL("image/png", []byte("hi"))
	// "hi" base64 is "aGk="
	if got != "data:image/png;base64,aGk=" {
		t.Fatalf("unexpected data url: %q", got)
	}
}

func TestBuildDocumentPart_PNGIsImageURL(t *testing.T) {
	p := BuildDocumentPart("image/png", []byte("x"))
	if p.Type != PartImageURL || p.ImageURL == nil || p.File != nil {
		t.Fatalf("expected image_url part, got %+v", p)
	}
	if !strings.HasPrefix(p.ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("image url prefix mismatch: %q", p.ImageURL.URL)
	}
}

func TestBuildDocumentPart_PDFIsFile(t *testing.T) {
	p := BuildDocumentPart("application/pdf", []byte("%PDF"))
	if p.Type != PartFile || p.File == nil || p.ImageURL != nil {
		t.Fatalf("expected file part, got %+v", p)
	}
	if p.File.Filename != pdfPartFilename {
		t.Fatalf("unexpected filename: %q", p.File.Filename)
	}
	if !strings.HasPrefix(p.File.FileData, "data:application/pdf;base64,") {
		t.Fatalf("file data prefix mismatch: %q", p.File.FileData)
	}
}

func TestClient_Complete_Success(t *testing.T) {
	var auth string
	var seen Request
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&seen)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{Choices: []Choice{{Message: ResponseMsg{Content: "done"}}}})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "tok", 0)
	out, err := c.Complete(context.Background(), Request{Model: "gpt-5", Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "done" {
		t.Fatalf("unexpected content: %q", out)
	}
	if auth != "Bearer tok" {
		t.Fatalf("auth header mismatch: %q", auth)
	}
	if seen.Model != "gpt-5" {
		t.Fatalf("model not forwarded: %q", seen.Model)
	}
}

func TestClient_Complete_OmitsAuthWhenNoKey(t *testing.T) {
	var auth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{Choices: []Choice{{Message: ResponseMsg{Content: "ok"}}}})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "", 0)
	if _, err := c.Complete(context.Background(), Request{Model: "m"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if auth != "" {
		t.Fatalf("expected no auth header, got %q", auth)
	}
}

func TestClient_Complete_Non200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "", 0)
	if _, err := c.Complete(context.Background(), Request{Model: "m"}); err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestClient_Complete_EmptyChoices(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{Choices: []Choice{}})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "", 0)
	if _, err := c.Complete(context.Background(), Request{Model: "m"}); err == nil {
		t.Fatal("expected error for empty choices")
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
