package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jo-hoe/gostwriter/internal/common"
)

// Uploader handles storing temporary uploads on disk.
type Uploader struct {
	baseDir string
}

var allowedImageMimes = map[string]string{
	common.MimeImagePNG:  ".png",
	common.MimeImageJPEG: ".jpg",
	common.MimeImageJPG:  ".jpg",
}

// NewUploader creates an uploader that stores to baseDir/uploads.
func NewUploader(baseDir string) *Uploader {
	return &Uploader{baseDir: filepath.Join(baseDir, common.UploadsDirName)}
}

// SaveMultipartImage validates and stores an uploaded image (png/jpg) to disk.
// It returns the absolute file path and a cleanup function to delete the file.
// The caller should always invoke the cleanup function when the file is no longer needed.
func (u *Uploader) SaveMultipartImage(fileHeader *multipart.FileHeader, maxBytes int64) (string, func() error, string, error) {
	if fileHeader == nil {
		return "", nil, "", fmt.Errorf("no file provided")
	}
	mimeType := fileHeader.Header.Get("Content-Type")
	// Some clients set application/octet-stream for uploads; treat it as unknown and fall back to extension.
	if mimeType == "" || strings.EqualFold(strings.TrimSpace(mimeType), "application/octet-stream") {
		// Fallback: try to detect by extension
		ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
		mimeType = mime.TypeByExtension(ext)
	}
	if !isAllowedImageMime(mimeType) {
		return "", nil, "", fmt.Errorf("unsupported content type: %s", mimeType)
	}

	if err := os.MkdirAll(u.baseDir, 0o755); err != nil {
		return "", nil, "", fmt.Errorf("ensure uploads dir: %w", err)
	}

	src, err := fileHeader.Open()
	if err != nil {
		return "", nil, "", fmt.Errorf("open uploaded file: %w", err)
	}
	defer func() { _ = src.Close() }()

	ext := pickExtension(mimeType, fileHeader.Filename)
	filename := fmt.Sprintf("%s%s", randomHex(16), ext)
	dstPath := filepath.Join(u.baseDir, filename)

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return "", nil, "", fmt.Errorf("create tmp file: %w", err)
	}
	defer func() {
		_ = dst.Close()
	}()

	limited := io.LimitReader(src, maxBytes)
	if _, err := io.Copy(dst, limited); err != nil {
		_ = os.Remove(dstPath)
		return "", nil, "", fmt.Errorf("copy upload: %w", err)
	}

	cleanup := func() error {
		return os.Remove(dstPath)
	}
	return dstPath, cleanup, mimeType, nil
}

func isAllowedImageMime(mimeType string) bool {
	mt := strings.ToLower(strings.TrimSpace(mimeType))
	_, ok := allowedImageMimes[mt]
	return ok
}

func pickExtension(mimeType, original string) string {
	mt := strings.ToLower(strings.TrimSpace(mimeType))
	if ext, ok := allowedImageMimes[mt]; ok {
		return ext
	}
	ext := strings.ToLower(filepath.Ext(original))
	if ext == "" {
		return ".bin"
	}
	return ext
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SuggestFilenameTimestamp returns a sanitized time usable in templates.
func SuggestFilenameTimestamp() time.Time {
	return time.Now().UTC()
}
