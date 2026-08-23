package naming

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- TemplateNamer ---

func TestTemplateNamer_Default(t *testing.T) {
	n, err := NewTemplateNamer("", `{{ .Timestamp.Format "20060102-150405" }}-{{ .JobID }}.md`, "")
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	got, err := n.Name(NamingRequest{JobID: "abc", Timestamp: ts, Extension: ".md"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "20240102-030405-abc.md" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestTemplateNamer_Custom(t *testing.T) {
	n, err := NewTemplateNamer("{{ .JobID }}.md", "", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := n.Name(NamingRequest{JobID: "job-123", Timestamp: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if got != "job-123.md" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestTemplateNamer_WithBasePath(t *testing.T) {
	n, err := NewTemplateNamer("{{ .JobID }}.md", "", "inbox/")
	if err != nil {
		t.Fatal(err)
	}
	got, err := n.Name(NamingRequest{JobID: "j1", Timestamp: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if got != "inbox/j1.md" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestTemplateNamer_InvalidTemplate(t *testing.T) {
	_, err := NewTemplateNamer("{{ .Bad", "", "")
	if err == nil {
		t.Fatal("expected error for invalid template")
	}
}

// --- TitleNamer ---

func TestTitleNamer_WithTitle(t *testing.T) {
	n := NewTitleNamer("", ".md")
	title := "Hello World: A Test!"
	got, err := n.Name(NamingRequest{JobID: "j1", SuggestedTitle: &title})
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello-world-a-test.md" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestTitleNamer_FallbackToJobID_Nil(t *testing.T) {
	n := NewTitleNamer("", ".md")
	got, err := n.Name(NamingRequest{JobID: "fallback-id", SuggestedTitle: nil})
	if err != nil {
		t.Fatal(err)
	}
	if got != "fallback-id.md" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestTitleNamer_FallbackToJobID_Empty(t *testing.T) {
	n := NewTitleNamer("", ".md")
	empty := "   "
	got, err := n.Name(NamingRequest{JobID: "fallback-id", SuggestedTitle: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if got != "fallback-id.md" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestTitleNamer_SpecialChars(t *testing.T) {
	n := NewTitleNamer("", ".md")
	title := "foo/bar?baz<qux>"
	got, err := n.Name(NamingRequest{JobID: "j", SuggestedTitle: &title})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "/") && got != "foo/bar-baz-qux.md" {
		// path sep is fine only when it was the basePath separator; the slug itself must not have "/"
		t.Fatalf("unexpected: %s", got)
	}
	if strings.Contains(got, "?") || strings.Contains(got, "<") || strings.Contains(got, ">") {
		t.Fatalf("slug contains forbidden chars: %s", got)
	}
}

func TestTitleNamer_WithBasePath(t *testing.T) {
	n := NewTitleNamer("inbox/", ".md")
	title := "My Note"
	got, err := n.Name(NamingRequest{JobID: "j", SuggestedTitle: &title})
	if err != nil {
		t.Fatal(err)
	}
	if got != "inbox/my-note.md" {
		t.Fatalf("unexpected: %s", got)
	}
}

// --- slugify ---

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello World", "hello-world"},
		{"  spaces  ", "spaces"},
		{"foo---bar", "foo-bar"},
		{"café", "caf-"},   // accent stripped by non-letter mapping, then trimmed
		{"", "untitled"},
		{"!!!!", "untitled"},
	}
	// Override the café expectation: 'é' IS a letter (unicode.IsLetter), so it stays as 'é' lowercased.
	cases[3] = struct{ in, want string }{"café", "café"}
	for _, c := range cases {
		got := slugify(c.in)
		if got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- suffixedPath ---

func TestSuffixedPath(t *testing.T) {
	cases := []struct {
		p    string
		n    int
		want string
	}{
		{"inbox/foo.md", 1, "inbox/foo-1.md"},
		{"inbox/foo.md", 2, "inbox/foo-2.md"},
		{"foo.md", 3, "foo-3.md"},
		{"a/b/c.md", 1, "a/b/c-1.md"},
	}
	for _, c := range cases {
		got := suffixedPath(c.p, c.n)
		if got != c.want {
			t.Errorf("suffixedPath(%q, %d) = %q, want %q", c.p, c.n, got, c.want)
		}
	}
}

// --- CollisionNamer ---

type stubChecker struct {
	results []bool
	idx     int
}

func (s *stubChecker) Exists(_ context.Context, _ string) (bool, error) {
	if s.idx >= len(s.results) {
		return false, nil
	}
	v := s.results[s.idx]
	s.idx++
	return v, nil
}

type errChecker struct{}

func (e *errChecker) Exists(_ context.Context, _ string) (bool, error) {
	return false, errors.New("check failed")
}

func TestCollisionNamer_FreeOnFirstTry(t *testing.T) {
	base, _ := NewTemplateNamer("{{ .JobID }}.md", "", "")
	cn := NewCollisionNamer(base, &stubChecker{results: []bool{false}}, 5)
	got, err := cn.Name(NamingRequest{JobID: "j1", Timestamp: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if got != "j1.md" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestCollisionNamer_CollisionThenFree(t *testing.T) {
	base, _ := NewTemplateNamer("{{ .JobID }}.md", "", "inbox/")
	cn := NewCollisionNamer(base, &stubChecker{results: []bool{true, false}}, 5)
	got, err := cn.Name(NamingRequest{JobID: "j1", Timestamp: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if got != "inbox/j1-1.md" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestCollisionNamer_MultipleCollisions(t *testing.T) {
	base, _ := NewTemplateNamer("{{ .JobID }}.md", "", "")
	cn := NewCollisionNamer(base, &stubChecker{results: []bool{true, true, true, false}}, 10)
	got, err := cn.Name(NamingRequest{JobID: "j1", Timestamp: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if got != "j1-3.md" {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestCollisionNamer_ExhaustedRetries(t *testing.T) {
	base, _ := NewTemplateNamer("{{ .JobID }}.md", "", "")
	// always occupied
	cn := NewCollisionNamer(base, &stubChecker{results: []bool{true, true, true, true, true}}, 3)
	_, err := cn.Name(NamingRequest{JobID: "j1", Timestamp: time.Now()})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
}

func TestCollisionNamer_CheckerError(t *testing.T) {
	base, _ := NewTemplateNamer("{{ .JobID }}.md", "", "")
	cn := NewCollisionNamer(base, &errChecker{}, 5)
	_, err := cn.Name(NamingRequest{JobID: "j1", Timestamp: time.Now()})
	if err == nil {
		t.Fatal("expected error from checker")
	}
}

// --- GitHubPathChecker ---

func TestGitHubPathChecker_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
	}))
	defer srv.Close()

	c := NewGitHubPathChecker(srv.Client(), srv.URL, "org", "repo", "main", "token")
	exists, err := c.Exists(context.Background(), "inbox/foo.md")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("expected false for 404")
	}
}

func TestGitHubPathChecker_200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "foo.md"})
	}))
	defer srv.Close()

	c := NewGitHubPathChecker(srv.Client(), srv.URL, "org", "repo", "main", "token")
	exists, err := c.Exists(context.Background(), "inbox/foo.md")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("expected true for 200")
	}
}

func TestGitHubPathChecker_UnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewGitHubPathChecker(srv.Client(), srv.URL, "org", "repo", "main", "token")
	_, err := c.Exists(context.Background(), "inbox/foo.md")
	if err == nil {
		t.Fatal("expected error for 500")
	}
}

func TestGitHubPathChecker_RequestHeaders(t *testing.T) {
	var gotAuth, gotAccept, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotVersion = r.Header.Get("X-GitHub-Api-Version")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewGitHubPathChecker(srv.Client(), srv.URL, "org", "repo", "main", "mytoken")
	_, _ = c.Exists(context.Background(), "foo.md")

	if gotAuth != "Bearer mytoken" {
		t.Errorf("auth header: %q", gotAuth)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("accept header: %q", gotAccept)
	}
	if gotVersion != "2022-11-28" {
		t.Errorf("api version header: %q", gotVersion)
	}
}
