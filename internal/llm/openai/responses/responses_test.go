package responses

import (
	"context"
	"encoding/json"
	"io"
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

func TestBuildDocumentPart_PNGIsInputImage(t *testing.T) {
	p := BuildDocumentPart("image/png", []byte("x"))
	if p.Type != PartInputImage {
		t.Fatalf("expected input_image part, got %+v", p)
	}
	if !strings.HasPrefix(p.ImageURL, "data:image/png;base64,") {
		t.Fatalf("image url prefix mismatch: %q", p.ImageURL)
	}
	if p.FileData != "" || p.Filename != "" {
		t.Fatalf("image part should not carry file fields: %+v", p)
	}
}

func TestBuildDocumentPart_PDFIsInputFile(t *testing.T) {
	p := BuildDocumentPart("application/pdf", []byte("%PDF"))
	if p.Type != PartInputFile {
		t.Fatalf("expected input_file part, got %+v", p)
	}
	if p.Filename != pdfPartFilename {
		t.Fatalf("unexpected filename: %q", p.Filename)
	}
	if !strings.HasPrefix(p.FileData, "data:application/pdf;base64,") {
		t.Fatalf("file data prefix mismatch: %q", p.FileData)
	}
	if p.ImageURL != "" {
		t.Fatalf("file part should not carry image_url: %+v", p)
	}
}

// TestClient_Complete_PDFInputFileOnTheWire is a regression guard for the PDF
// transcription path. The chat-completions route on a compatible gateway
// rejects inline "file" parts, but the Responses API accepts inline-base64 PDFs
// as "input_file" parts (verified end-to-end). This test pins the serialization
// so a future refactor cannot silently drop or rename the file fields.
func TestClient_Complete_PDFInputFileOnTheWire(t *testing.T) {
	var rawBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{Output: []OutputItem{
			{Type: outputTypeMessage, Content: []OutputContent{{Type: outputTypeOutputText, Text: "ok"}}},
		}})
	}))
	defer ts.Close()

	part := BuildDocumentPart("application/pdf", []byte("%PDF-1.4 regression"))
	c := NewClient(ts.URL, "tok", 0)
	_, err := c.Complete(context.Background(), Request{
		Model: "gpt-5",
		Input: []Item{{Role: RoleUser, Content: []Part{part}}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Decode generically so we inspect exactly what crossed the wire.
	var sent struct {
		Input []struct {
			Content []struct {
				Type     string `json:"type"`
				Filename string `json:"filename"`
				FileData string `json:"file_data"`
			} `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(rawBody, &sent); err != nil {
		t.Fatalf("unmarshal sent body: %v (raw=%s)", err, rawBody)
	}
	if len(sent.Input) != 1 || len(sent.Input[0].Content) != 1 {
		t.Fatalf("unexpected input/content shape: %s", rawBody)
	}
	cp := sent.Input[0].Content[0]
	if cp.Type != string(PartInputFile) {
		t.Fatalf("expected type %q, got %q", PartInputFile, cp.Type)
	}
	if cp.Filename != pdfPartFilename {
		t.Fatalf("filename mismatch: %q", cp.Filename)
	}
	if !strings.HasPrefix(cp.FileData, "data:application/pdf;base64,") {
		t.Fatalf("file_data prefix mismatch: %q", cp.FileData)
	}
}

// TestClient_Complete_ImageInputOnTheWire pins the image part serialization:
// an "input_image" part carrying a plain data-URL string in image_url.
func TestClient_Complete_ImageInputOnTheWire(t *testing.T) {
	var rawBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{Output: []OutputItem{
			{Type: outputTypeMessage, Content: []OutputContent{{Type: outputTypeOutputText, Text: "ok"}}},
		}})
	}))
	defer ts.Close()

	part := BuildDocumentPart("image/png", []byte("pngbytes"))
	c := NewClient(ts.URL, "", 0)
	if _, err := c.Complete(context.Background(), Request{
		Model: "gpt-5",
		Input: []Item{{Role: RoleUser, Content: []Part{part}}},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var sent struct {
		Input []struct {
			Content []struct {
				Type     string `json:"type"`
				ImageURL string `json:"image_url"`
			} `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(rawBody, &sent); err != nil {
		t.Fatalf("unmarshal sent body: %v (raw=%s)", err, rawBody)
	}
	cp := sent.Input[0].Content[0]
	if cp.Type != string(PartInputImage) {
		t.Fatalf("expected type %q, got %q", PartInputImage, cp.Type)
	}
	if !strings.HasPrefix(cp.ImageURL, "data:image/png;base64,") {
		t.Fatalf("image_url prefix mismatch: %q", cp.ImageURL)
	}
}

func TestClient_Complete_Success(t *testing.T) {
	var auth string
	var seen Request
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/responses" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&seen)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{Output: []OutputItem{
			{Type: outputTypeMessage, Content: []OutputContent{{Type: outputTypeOutputText, Text: "done"}}},
		}})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "tok", 0)
	out, err := c.Complete(context.Background(), Request{Model: "gpt-5", Input: []Item{{Role: RoleUser, Content: []Part{TextPart("hi")}}}})
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

// TestClient_Complete_SkipsReasoning verifies that non-message output items
// (e.g. "reasoning") are skipped and multiple output_text blocks concatenated.
func TestClient_Complete_SkipsReasoning(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{Output: []OutputItem{
			{Type: "reasoning", Content: []OutputContent{{Type: "reasoning_text", Text: "ignore me"}}},
			{Type: outputTypeMessage, Content: []OutputContent{
				{Type: outputTypeOutputText, Text: "Hello "},
				{Type: outputTypeOutputText, Text: "Gostwriter"},
			}},
		}})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "", 0)
	out, err := c.Complete(context.Background(), Request{Model: "m"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "Hello Gostwriter" {
		t.Fatalf("expected concatenated message text, got %q", out)
	}
}

func TestClient_Complete_OmitsAuthWhenNoKey(t *testing.T) {
	var auth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{Output: []OutputItem{
			{Type: outputTypeMessage, Content: []OutputContent{{Type: outputTypeOutputText, Text: "ok"}}},
		}})
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

func TestClient_Complete_EmptyOutput(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Response{Output: []OutputItem{}})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "", 0)
	if _, err := c.Complete(context.Background(), Request{Model: "m"}); err == nil {
		t.Fatal("expected error for empty output")
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

func TestOutputTextPart_Type(t *testing.T) {
	p := OutputTextPart("hello")
	if p.Type != PartOutputText {
		t.Fatalf("expected type %q, got %q", PartOutputText, p.Type)
	}
	if p.Text != "hello" {
		t.Fatalf("expected text %q, got %q", "hello", p.Text)
	}
	// Ensure the type serialises correctly on the wire — the Responses API
	// requires "output_text" (not "input_text") for assistant-role content.
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"output_text"`) {
		t.Fatalf("expected output_text in JSON, got %s", b)
	}
}
