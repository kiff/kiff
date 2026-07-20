package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubBrowserAndSleep neutralizes the browser-open and poll delay for
// tests, restoring them on cleanup.
func stubBrowserAndSleep(t *testing.T) {
	t.Helper()
	origOpen, origSleep := openBrowserFn, sleepFn
	openBrowserFn = func(string) error { return nil }
	sleepFn = func(time.Duration) {}
	t.Cleanup(func() { openBrowserFn = origOpen; sleepFn = origSleep })
}

func TestAuthLogin_FullFlow(t *testing.T) {
	stubBrowserAndSleep(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KIFF_TOKEN", "")
	t.Setenv("KIFF_CLOUD_URL", "")

	var mu sync.Mutex
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/device/start":
			_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"BCDF-GHJK","verification_uri":"https://x/auth/device","verification_uri_complete":"https://x/auth/device?code=BCDF-GHJK","expires_in":600,"interval":0}`))
		case "/v1/auth/device/token":
			mu.Lock()
			polls++
			n := polls
			mu.Unlock()
			if n == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"kiff_dev_tok","tenant":"tenant-1","subject":"user-1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var buf bytes.Buffer
	if err := authLogin(&buf, []string{"-endpoint", srv.URL}); err != nil {
		t.Fatalf("authLogin: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "BCDF-GHJK") || !strings.Contains(out, "Signed in as user-1") {
		t.Errorf("login output missing expected content:\n%s", out)
	}
	// Credential persisted and readable by resolveToken.
	if tok, err := resolveToken(""); err != nil || tok != "kiff_dev_tok" {
		t.Fatalf("resolveToken after login = %q, err=%v", tok, err)
	}
	// Endpoint persisted to ~/.kiff/config.
	cfg, _ := os.ReadFile(filepath.Join(home, ".kiff", "config"))
	if !strings.Contains(string(cfg), "endpoint") || !strings.Contains(string(cfg), srv.URL) {
		t.Errorf("endpoint not persisted to config: %q", string(cfg))
	}
	// Credential file is 0600.
	info, err := os.Stat(filepath.Join(home, ".kiff", "credentials"))
	if err != nil {
		t.Fatalf("stat credentials: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("credentials mode = %o, want 600", info.Mode().Perm())
	}
}

func TestPollDeviceToken_Cases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		status    int
		body      string
		wantRetry string
		wantErr   bool
		wantToken string
	}{
		{"pending", 400, `{"error":"authorization_pending"}`, "authorization_pending", false, ""},
		{"slow_down", 400, `{"error":"slow_down"}`, "slow_down", false, ""},
		{"expired", 400, `{"error":"expired_token"}`, "", true, ""},
		{"denied", 400, `{"error":"access_denied"}`, "", true, ""},
		{"success", 200, `{"access_token":"kiff_dev_x","subject":"s","tenant":"t"}`, "", false, "kiff_dev_x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			tok, retry, err := pollDeviceToken(srv.Client(), srv.URL, "dc")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if retry != tc.wantRetry {
				t.Errorf("retry = %q, want %q", retry, tc.wantRetry)
			}
			if tok.AccessToken != tc.wantToken {
				t.Errorf("token = %q, want %q", tok.AccessToken, tc.wantToken)
			}
		})
	}
}

func TestAuthStatus_NotSignedIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KIFF_TOKEN", "")
	t.Setenv("KIFF_CLOUD_URL", "")
	var buf bytes.Buffer
	if err := authStatus(&buf, []string{"-endpoint", "https://api.example.com"}); err != nil {
		t.Fatalf("authStatus: %v", err)
	}
	if !strings.Contains(buf.String(), "not signed in") {
		t.Errorf("want not-signed-in message, got %q", buf.String())
	}
}

func TestAuthStatus_SignedIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KIFF_TOKEN", "")
	t.Setenv("KIFF_CLOUD_URL", "")
	if err := writeKiffCredential("kiff_dev_tok"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/me" {
			_, _ = w.Write([]byte(`{"subject":"user-1","tenant_id":"t1","tenant_slug":"demo"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	var buf bytes.Buffer
	if err := authStatus(&buf, []string{"-endpoint", srv.URL}); err != nil {
		t.Fatalf("authStatus: %v", err)
	}
	if !strings.Contains(buf.String(), "signed in as user-1") || !strings.Contains(buf.String(), "demo") {
		t.Errorf("status output = %q", buf.String())
	}
}

func TestAuthLogout_RevokesAndForgets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KIFF_TOKEN", "")
	t.Setenv("KIFF_CLOUD_URL", "")
	if err := writeKiffCredential("kiff_dev_tok"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/device/logout" {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	var buf bytes.Buffer
	if err := authLogout(&buf, []string{"-endpoint", srv.URL}); err != nil {
		t.Fatalf("authLogout: %v", err)
	}
	if gotAuth != "Bearer kiff_dev_tok" {
		t.Errorf("logout Authorization = %q", gotAuth)
	}
	if _, err := os.Stat(filepath.Join(home, ".kiff", "credentials")); !os.IsNotExist(err) {
		t.Errorf("credential should be forgotten; stat err = %v", err)
	}
	if !strings.Contains(buf.String(), "signed out") {
		t.Errorf("logout output = %q", buf.String())
	}
}

func TestWriteKiffConfigValue_PreservesOtherKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".kiff", "config")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte("other = keep\nendpoint = old\n"), 0o600)
	if err := writeKiffConfigValue("endpoint", "https://new"); err != nil {
		t.Fatalf("writeKiffConfigValue: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "other = keep") {
		t.Errorf("other key not preserved: %q", string(got))
	}
	if !strings.Contains(string(got), "endpoint = https://new") || strings.Contains(string(got), "old") {
		t.Errorf("endpoint not updated: %q", string(got))
	}
}
