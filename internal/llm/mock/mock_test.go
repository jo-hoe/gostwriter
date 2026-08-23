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

	md, err := c.TranscribeImage(ctx, img, "image/png")
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

func TestMockLLM_GenerateTitle_ExtractsFirstLine(t *testing.T) {
	c := New(config.MockSettings{Prefix: "Mock"})
	title, err := c.GenerateTitle(context.Background(), "# My Heading\n\nsome body text")
	if err != nil {
		t.Fatal(err)
	}
	if title != "Mock My Heading" {
		t.Fatalf("unexpected title: %q", title)
	}
}

func TestMockLLM_GenerateTitle_SkipsBlankLines(t *testing.T) {
	c := New(config.MockSettings{Prefix: "Mock"})
	title, err := c.GenerateTitle(context.Background(), "\n\n## Second Heading")
	if err != nil {
		t.Fatal(err)
	}
	if title != "Mock Second Heading" {
		t.Fatalf("unexpected title: %q", title)
	}
}

func TestMockLLM_GenerateTitle_EmptyMarkdown(t *testing.T) {
	c := New(config.MockSettings{Prefix: "Mock"})
	title, err := c.GenerateTitle(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if title != "Mock Untitled" {
		t.Fatalf("unexpected title: %q", title)
	}
}

func TestMockLLM_GenerateTitle_RespectsContextCancel(t *testing.T) {
	c := New(config.MockSettings{Delay: 200 * time.Millisecond, Prefix: "Mock"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.GenerateTitle(ctx, "some text")
	if err == nil {
		t.Fatal("expected context cancellation error")
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

	_, err := c.TranscribeImage(ctx, bytes.NewBufferString("x"), "image/png")
	if err == nil {
		t.Fatalf("expected context cancellation error")
	}
}
