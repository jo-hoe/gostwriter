package naming

import (
	"context"
	"fmt"
	"path"
	"strings"
)

// CollisionNamer wraps a FileNamer and appends a numeric suffix (-1, -2, …)
// until PathChecker confirms the chosen path is free.
type CollisionNamer struct {
	inner   FileNamer
	checker PathChecker
	maxTry  int
}

func NewCollisionNamer(inner FileNamer, checker PathChecker, maxTry int) *CollisionNamer {
	if maxTry <= 0 {
		maxTry = 100
	}
	return &CollisionNamer{inner: inner, checker: checker, maxTry: maxTry}
}

// Name satisfies FileNamer using context.Background.
func (n *CollisionNamer) Name(req NamingRequest) (string, error) {
	return n.NameWithContext(context.Background(), req)
}

// NameWithContext is the preferred entry point when a live context is available.
func (n *CollisionNamer) NameWithContext(ctx context.Context, req NamingRequest) (string, error) {
	base, err := n.inner.Name(req)
	if err != nil {
		return "", err
	}
	candidate := base
	for i := 1; i <= n.maxTry; i++ {
		exists, err := n.checker.Exists(ctx, candidate)
		if err != nil {
			return "", fmt.Errorf("collision check: %w", err)
		}
		if !exists {
			return candidate, nil
		}
		candidate = suffixedPath(base, i)
	}
	return "", fmt.Errorf("could not find a free filename after %d attempts (base %q)", n.maxTry, base)
}

// suffixedPath inserts "-N" before the final extension.
// e.g. "inbox/foo.md" → "inbox/foo-1.md"
func suffixedPath(p string, n int) string {
	dir := path.Dir(p)
	base := path.Base(p)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	newBase := fmt.Sprintf("%s-%d%s", stem, n, ext)
	if dir == "." {
		return newBase
	}
	return dir + "/" + newBase
}
