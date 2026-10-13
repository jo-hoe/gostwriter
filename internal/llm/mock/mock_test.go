package mock

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jo-hoe/gostwriter/internal/config"
)

func TestMockLLM_TranscribeImage(t *testing.T) {
	cfg := config.MockSettings{
		Delay:  0,
		Prefix: "MockPrefix",
	}
	c := New(cfg)

	img := bytes.NewBufferString("fakeimagedata")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	md, _, err := c.TranscribeImage(ctx, img, "image/png")
	if err != nil {
		t.Fatalf("TranscribeImage error: %v", err)
	}
	if !strings.Contains(md, "MockPrefix") {
		t.Fatalf("TranscribeImage missing prefix, got: %q", md)
	}
	if !strings.Contains(md, "image/png") {
		t.Fatalf("TranscribeImage missing mime info, got: %q", md)
	}
}

func TestMockLLM_RespectsContextCancel(t *testing.T) {
	cfg := config.MockSettings{
		Delay:  200 * time.Millisecond,
		Prefix: "x",
	}
	c := New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, _, err := c.TranscribeImage(ctx, bytes.NewBufferString("x"), "image/png")
	if err == nil {
		t.Fatalf("expected context cancellation error")
	}
}

func TestMockLLM_SuggestFilename(t *testing.T) {
	c := New(config.MockSettings{Prefix: "Mock"})
	slug, err := c.SuggestFilename(context.Background(), mockConversation{})
	if err != nil {
		t.Fatal(err)
	}
	if slug == "" {
		t.Fatal("expected non-empty slug")
	}
}

func TestMockLLM_SuggestFilename_RespectsContextCancel(t *testing.T) {
	c := New(config.MockSettings{Delay: 200 * time.Millisecond, Prefix: "Mock"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.SuggestFilename(ctx, mockConversation{})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}
