package storage

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
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

// signature maps a magic-number prefix to the canonical MIME type and file
// extension it represents. The upload type is decided solely from the file
// content; the client-declared Content-Type and filename are not trusted.
type signature struct {
	magic []byte
	mime  string
	ext   string
}

// signatures are checked in order; the first prefix match wins.
var signatures = []signature{
	{magic: []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, mime: common.MimeImagePNG, ext: ".png"},
	{magic: []byte{0xFF, 0xD8, 0xFF}, mime: common.MimeImageJPEG, ext: ".jpg"},
	{magic: []byte("%PDF-"), mime: common.MimeApplicationPDF, ext: ".pdf"},
}

// sniffLen is the number of leading bytes read to detect the file type. It must
// be at least as long as the longest magic number in signatures.
const sniffLen = 8

// NewUploader creates an uploader that stores to baseDir/uploads.
func NewUploader(baseDir string) *Uploader {
	return &Uploader{baseDir: filepath.Join(baseDir, common.UploadsDirName)}
}

// SaveMultipartImage validates and stores an uploaded document (png/jpg/pdf) to disk.
// The upload type is determined by sniffing the file content (magic numbers);
// the client's Content-Type header and filename extension are ignored. Content
// that does not match a supported signature is rejected.
// It returns the absolute file path, a cleanup function to delete the file, and
// the detected MIME type. The caller should always invoke the cleanup function
// when the file is no longer needed.
func (u *Uploader) SaveMultipartImage(fileHeader *multipart.FileHeader, maxBytes int64) (string, func() error, string, error) {
	if fileHeader == nil {
		return "", nil, "", fmt.Errorf("no file provided")
	}

	src, err := fileHeader.Open()
	if err != nil {
		return "", nil, "", fmt.Errorf("open uploaded file: %w", err)
	}
	defer func() { _ = src.Close() }()

	// Buffer the stream so we can peek the leading bytes for detection without
	// consuming them, then copy the full content to disk.
	br := bufio.NewReaderSize(src, sniffLen)
	head, err := br.Peek(sniffLen)
	// io.EOF/ErrUnexpectedEOF means the file is shorter than sniffLen; detect on
	// whatever we got and surface any other read error.
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", nil, "", fmt.Errorf("read upload header: %w", err)
	}

	sig, ok := detectSignature(head)
	if !ok {
		return "", nil, "", fmt.Errorf("unsupported or malformed file content")
	}

	if err := os.MkdirAll(u.baseDir, 0o750); err != nil {
		return "", nil, "", fmt.Errorf("ensure uploads dir: %w", err)
	}

	filename := fmt.Sprintf("%s%s", randomHex(16), sig.ext)
	dstPath := filepath.Join(u.baseDir, filename)
	// Ensure the destination path stays within the base uploads directory to prevent path traversal.
	base := filepath.Clean(u.baseDir)
	cleanDst := filepath.Clean(dstPath)
	if rel, err := filepath.Rel(base, cleanDst); err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", nil, "", fmt.Errorf("invalid destination path")
	}

	dst, err := os.OpenFile(cleanDst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600) // #nosec G304 - path validated against base uploads dir above
	if err != nil {
		return "", nil, "", fmt.Errorf("create tmp file: %w", err)
	}
	defer func() {
		_ = dst.Close()
	}()

	// Copy the full content (including the peeked header) from the buffered reader.
	limited := io.LimitReader(br, maxBytes)
	if _, err := io.Copy(dst, limited); err != nil {
		_ = os.Remove(cleanDst)
		return "", nil, "", fmt.Errorf("copy upload: %w", err)
	}

	cleanup := func() error {
		return os.Remove(cleanDst)
	}
	return cleanDst, cleanup, sig.mime, nil
}

// detectSignature returns the signature whose magic number prefixes head.
func detectSignature(head []byte) (signature, bool) {
	for _, s := range signatures {
		if bytes.HasPrefix(head, s.magic) {
			return s, true
		}
	}
	return signature{}, false
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
