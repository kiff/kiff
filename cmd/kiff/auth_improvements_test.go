package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteKiffConfigValue_PreservesBlankLinesAndComments(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".kiff", "config")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte("# my config\n\nother = keep\n\nendpoint = old\n"), 0o600)

	// Rewrite twice to prove blank lines don't accumulate or vanish.
	for i := 0; i < 2; i++ {
		if err := writeKiffConfigValue("endpoint", "https://new"); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	got, _ := os.ReadFile(path)
	s := string(got)
	if !strings.Contains(s, "# my config") {
		t.Errorf("comment dropped: %q", s)
	}
	if !strings.Contains(s, "other = keep") {
		t.Errorf("other key dropped: %q", s)
	}
	if !strings.Contains(s, "\n\n") {
		t.Errorf("blank lines not preserved: %q", s)
	}
	if !strings.Contains(s, "endpoint = https://new") || strings.Contains(s, "old") {
		t.Errorf("endpoint not updated: %q", s)
	}
	// No runaway trailing blank lines: at most one trailing newline.
	if strings.HasSuffix(s, "\n\n") {
		t.Errorf("trailing blank lines accumulated: %q", s)
	}
}

func TestAuthStatus_SignedInNoEndpoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KIFF_TOKEN", "")
	t.Setenv("KIFF_CLOUD_URL", "")
	if err := writeKiffCredential("kiff_dev_tok"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	var buf bytes.Buffer
	// No -endpoint, no env, no config endpoint → token present but no
	// endpoint. Should report signed-in-but-no-endpoint, not "no endpoint".
	if err := authStatus(&buf, nil); err != nil {
		t.Fatalf("authStatus: %v", err)
	}
	if !strings.Contains(buf.String(), "no endpoint is configured") {
		t.Errorf("want signed-in-no-endpoint message, got %q", buf.String())
	}
}

func TestAuthLogout_All(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KIFF_TOKEN", "")
	t.Setenv("KIFF_CLOUD_URL", "")
	if err := writeKiffCredential("kiff_dev_tok"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/device/logout" {
			gotQuery = r.URL.RawQuery
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	if err := authLogout(&buf, []string{"-endpoint", srv.URL, "-all"}); err != nil {
		t.Fatalf("authLogout --all: %v", err)
	}
	if gotQuery != "all=true" {
		t.Errorf("logout query = %q, want all=true", gotQuery)
	}
	if !strings.Contains(buf.String(), "signed out everywhere") {
		t.Errorf("output = %q", buf.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".kiff", "credentials")); !os.IsNotExist(err) {
		t.Errorf("credential should be forgotten; err = %v", err)
	}
}
