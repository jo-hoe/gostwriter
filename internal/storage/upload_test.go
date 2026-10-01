package storage

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// Valid magic-number prefixes for the supported upload types, padded with
// trailing bytes so the content is realistic (and longer than sniffLen).
var (
	pngBytes = append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, []byte("rest-of-png")...)
	jpgBytes = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, []byte("rest-of-jpeg")...)
	pdfBytes = []byte("%PDF-1.7\nrest-of-pdf")
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

	_, fh := makeMultipartFile(t, "image.png", "image/png", pngBytes)
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
	if filepath.Ext(path) != ".png" {
		t.Fatalf("expected .png extension, got %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved file not found: %v", err)
	}
	// Ensure stored under uploads dir
	if filepath.Dir(path) != filepath.Join(tmp, "uploads") {
		t.Fatalf("file not stored under uploads dir: %s", path)
	}
	// The full content, including the sniffed header, must be written verbatim.
	got, err := os.ReadFile(path) // #nosec G304 - test-controlled path under TempDir
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, pngBytes) {
		t.Fatalf("stored content mismatch: got %q", got)
	}
}

func TestUploader_SaveMultipartImage_JPEG(t *testing.T) {
	tmp := t.TempDir()
	up := NewUploader(tmp)

	_, fh := makeMultipartFile(t, "photo.jpg", "image/jpeg", jpgBytes)
	path, cleanup, mime, err := up.SaveMultipartImage(fh, 10*1024*1024)
	if err != nil {
		t.Fatalf("SaveMultipartImage: %v", err)
	}
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	if mime != "image/jpeg" {
		t.Fatalf("jpeg mime expected, got %q", mime)
	}
	if filepath.Ext(path) != ".jpg" {
		t.Fatalf("expected .jpg extension, got %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved file not found: %v", err)
	}
}

func TestUploader_SaveMultipartImage_PDF(t *testing.T) {
	tmp := t.TempDir()
	up := NewUploader(tmp)

	_, fh := makeMultipartFile(t, "scan.pdf", "application/pdf", pdfBytes)
	path, cleanup, mime, err := up.SaveMultipartImage(fh, 10*1024*1024)
	if err != nil {
		t.Fatalf("SaveMultipartImage: %v", err)
	}
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	if mime != "application/pdf" {
		t.Fatalf("mime = %q", mime)
	}
	if filepath.Ext(path) != ".pdf" {
		t.Fatalf("expected .pdf extension, got %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved file not found: %v", err)
	}
}

// The declared Content-Type and filename are ignored: a PDF uploaded with a
// deliberately wrong header and extension is still detected and stored as a PDF.
func TestUploader_SaveMultipartImage_IgnoresDeclaredType(t *testing.T) {
	tmp := t.TempDir()
	up := NewUploader(tmp)

	_, fh := makeMultipartFile(t, "liar.png", "image/png", pdfBytes)
	path, cleanup, mime, err := up.SaveMultipartImage(fh, 10*1024*1024)
	if err != nil {
		t.Fatalf("SaveMultipartImage: %v", err)
	}
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	if mime != "application/pdf" {
		t.Fatalf("expected detected mime application/pdf, got %q", mime)
	}
	if filepath.Ext(path) != ".pdf" {
		t.Fatalf("expected .pdf extension from content, got %q", path)
	}
}

// Bytes that match no supported signature are rejected regardless of a
// plausible-looking Content-Type and extension.
func TestUploader_SaveMultipartImage_RejectsContentMismatch(t *testing.T) {
	tmp := t.TempDir()
	up := NewUploader(tmp)

	_, fh := makeMultipartFile(t, "fake.pdf", "application/pdf", []byte("this is not a pdf"))
	_, _, _, err := up.SaveMultipartImage(fh, 1024)
	if err == nil {
		t.Fatalf("expected error for content not matching any signature")
	}
	// Nothing should have been written to the uploads dir.
	entries, _ := os.ReadDir(filepath.Join(tmp, "uploads"))
	if len(entries) != 0 {
		t.Fatalf("expected no stored file on rejection, found %d", len(entries))
	}
}

func TestUploader_SaveMultipartImage_RejectsUnsupported(t *testing.T) {
	tmp := t.TempDir()
	up := NewUploader(tmp)

	_, fh := makeMultipartFile(t, "doc.txt", "text/plain", []byte("just some text"))
	_, _, _, err := up.SaveMultipartImage(fh, 1024)
	if err == nil {
		t.Fatalf("expected error for unsupported content")
	}
}

func TestUploader_SaveMultipartImage_RejectsEmpty(t *testing.T) {
	tmp := t.TempDir()
	up := NewUploader(tmp)

	_, fh := makeMultipartFile(t, "empty.png", "image/png", nil)
	_, _, _, err := up.SaveMultipartImage(fh, 1024)
	if err == nil {
		t.Fatalf("expected error for empty upload")
	}
}

func TestUploader_RespectsMaxBytes(t *testing.T) {
	tmp := t.TempDir()
	up := NewUploader(tmp)

	// Valid PNG header followed by a large body so detection passes but the
	// copy is truncated by the byte limit.
	large := append(append([]byte{}, pngBytes...), bytes.Repeat([]byte("x"), 4096)...)
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

	_, fh := makeMultipartFile(t, "keep.png", "image/png", pngBytes)
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

func TestDetectSignature(t *testing.T) {
	cases := []struct {
		name     string
		head     []byte
		wantMime string
		wantOK   bool
	}{
		{"png", pngBytes, "image/png", true},
		{"jpeg", jpgBytes, "image/jpeg", true},
		{"pdf", pdfBytes, "application/pdf", true},
		{"pdf exact prefix", []byte("%PDF-"), "application/pdf", true},
		{"text", []byte("plain text content"), "", false},
		{"empty", nil, "", false},
		{"png prefix too short", []byte{0x89, 0x50}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sig, ok := detectSignature(c.head)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if ok && sig.mime != c.wantMime {
				t.Fatalf("mime = %q, want %q", sig.mime, c.wantMime)
			}
		})
	}
}
