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
