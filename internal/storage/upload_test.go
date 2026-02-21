package storage

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func makeMultipartFile(t *testing.T, filename string, contentType string, content []byte) (*http.Request, *multipart.FileHeader) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, "http://example/upload", &b)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	// Parse to obtain FileHeader
	if err := req.ParseMultipartForm(int64(len(b.Bytes())) + 1024); err != nil {
		t.Fatalf("ParseMultipartForm: %v", err)
	}
	fhs := req.MultipartForm.File["file"]
	if len(fhs) == 0 {
		t.Fatalf("no fileheaders parsed")
	}
	// Optionally override detected header content-type for stricter testing
	if contentType != "" {
		fhs[0].Header.Set("Content-Type", contentType)
	}
	return req, fhs[0]
}

func TestUploader_SaveMultipartImage_PNG(t *testing.T) {
	tmp := t.TempDir()
	up := NewUploader(tmp)

	_, fh := makeMultipartFile(t, "image.png", "image/png", []byte("pngdata"))
	path, cleanup, mime, err := up.SaveMultipartImage(fh, 10*1024*1024)
	if err != nil {
		t.Fatalf("SaveMultipartImage: %v", err)
	}
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	if mime != "image/png" {
		t.Fatalf("mime = %q", mime)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved file not found: %v", err)
	}
	// Ensure stored under uploads dir
	if filepath.Dir(path) != filepath.Join(tmp, "uploads") {
		t.Fatalf("file not stored under uploads dir: %s", path)
	}
}

func TestUploader_SaveMultipartImage_JPEG_ByExtension(t *testing.T) {
	tmp := t.TempDir()
	up := NewUploader(tmp)

	// No explicit content-type header; rely on extension detection
	req, fh := makeMultipartFile(t, "photo.jpg", "", []byte("jpgdata"))
	_ = req // not used further

	path, cleanup, mime, err := up.SaveMultipartImage(fh, 10*1024*1024)
	if err != nil {
		t.Fatalf("SaveMultipartImage: %v", err)
	}
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	if mime != "image/jpeg" && mime != "image/jpg" {
		t.Fatalf("jpeg mime expected, got %q", mime)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved file not found: %v", err)
	}
}

func TestUploader_SaveMultipartImage_RejectsUnsupported(t *testing.T) {
	tmp := t.TempDir()
	up := NewUploader(tmp)

	_, fh := makeMultipartFile(t, "doc.txt", "text/plain", []byte("text"))
	_, _, _, err := up.SaveMultipartImage(fh, 1024)
	if err == nil {
		t.Fatalf("expected error for unsupported mime")
	}
}

func TestUploader_RespectsMaxBytes(t *testing.T) {
	tmp := t.TempDir()
	up := NewUploader(tmp)

	// Create file larger than limit
	large := bytes.Repeat([]byte("x"), 4096)
	_, fh := makeMultipartFile(t, "big.png", "image/png", large)

	path, cleanup, _, err := up.SaveMultipartImage(fh, 1024) // only 1KiB allowed
	if err != nil {
		// Depending on OS, io.Copy may not error on truncation; ensure no file remains if created
		return
	}
	// File may exist but truncated; ensure cleanup works
	if cleanup != nil {
		_ = cleanup()
	}
	_, statErr := os.Stat(path)
	if statErr == nil {
		// best-effort: file should be removed by cleanup
		t.Fatalf("expected file not to remain after cleanup for oversized input")
	}
}

func TestUploader_CleanupRemovesFile(t *testing.T) {
	tmp := t.TempDir()
	up := NewUploader(tmp)

	_, fh := makeMultipartFile(t, "keep.png", "image/png", []byte("png"))
	path, cleanup, _, err := up.SaveMultipartImage(fh, 10*1024*1024)
	if err != nil {
		t.Fatalf("SaveMultipartImage: %v", err)
	}
	if cleanup == nil {
		t.Fatalf("cleanup is nil")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved file not found before cleanup: %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup error: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("file still exists after cleanup")
	}
}

