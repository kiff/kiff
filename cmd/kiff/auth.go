package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// auth.go — `kiff auth login | status | logout` (RFC 034-B slice 3):
// the device-authorization client that fills the credential slots the
// other cloud-facing commands (apply, the operator set) already read.
//
//	kiff auth login    device flow -> store a developer session
//	kiff auth status    who am I / where am I signed in
//	kiff auth logout    revoke this device's session + forget it
//
// Cloud-agnostic (RFC 034 Decision 1): the endpoint resolves the same
// way as apply (flag -> KIFF_CLOUD_URL -> ~/.kiff/config -> build
// default), so nothing names a hosted instance in source.
//
// Storage: the developer session is written to ~/.kiff/credentials at
// 0600 — the same file resolveToken already reads — and the endpoint
// to ~/.kiff/config, so a later `kiff apply` / `kiff domains list`
// authenticates with no extra flags. A real OS keychain (Keychain /
// libsecret / Credential Manager) is a dependency-free-blocked
// follow-up; the 0600 file is the interim per RFC 034-A.

// openBrowserFn opens a URL in the user's browser. A package var so
// tests can stub it. Best-effort: a failure is not fatal (the URL is
// always printed too).
var openBrowserFn = openBrowser

// sleepFn is the poll delay, overridable in tests to avoid real waits.
var sleepFn = time.Sleep

func runAuth(out io.Writer, args []string) error {
	sub := ""
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		rest = args[1:]
	}
	switch sub {
	case "login":
		return authLogin(out, rest)
	case "status":
		return authStatus(out, rest)
	case "logout":
		return authLogout(out, rest)
	default:
		authUsage()
		return fmt.Errorf("unknown or missing subcommand %q (want login | status | logout)", sub)
	}
}

func authUsage() {
	fmt.Fprintln(os.Stderr, "kiff auth login     Sign in to a KIFF cloud via the device flow")
	fmt.Fprintln(os.Stderr, "kiff auth status    Show the current developer session")
	fmt.Fprintln(os.Stderr, "kiff auth logout    Revoke this device's session and forget it")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Endpoint: -endpoint URL > KIFF_CLOUD_URL > ~/.kiff/config > build default.")
}

// --- login ---

type deviceStartResp struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type deviceTokenResp struct {
	AccessToken string `json:"access_token"`
	Tenant      string `json:"tenant"`
	Subject     string `json:"subject"`
	Error       string `json:"error"`
}

func authLogin(out io.Writer, args []string) error {
	fs := flag.NewFlagSet("auth login", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	endpoint := fs.String("endpoint", "", "Base URL of the KIFF cloud")
	timeout := fs.Duration("timeout", 30*time.Second, "per-request HTTP timeout")
	if err := fs.Parse(args); err != nil {
		authUsage()
		return helpOrErr(err)
	}
	base, err := resolveEndpoint(*endpoint)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: *timeout}

	start, err := deviceStart(client, base)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "To sign in, open:\n\n    %s\n\n", verificationTarget(start))
	fmt.Fprintf(out, "and confirm this code:\n\n    %s\n\n", start.UserCode)
	if err := openBrowserFn(verificationTarget(start)); err != nil {
		fmt.Fprintln(out, "(couldn't open the browser automatically — open the link above)")
	}
	fmt.Fprintln(out, "Waiting for approval...")

	interval := time.Duration(start.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	expiresIn := start.ExpiresIn
	if expiresIn < 60 {
		expiresIn = 60
	}
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)

	for time.Now().Before(deadline) {
		sleepFn(interval)
		tok, retry, err := pollDeviceToken(client, base, start.DeviceCode)
		if err != nil {
			return err
		}
		if retry == "slow_down" {
			interval += 5 * time.Second
			continue
		}
		if retry == "authorization_pending" {
			continue
		}
		// Success.
		if err := writeKiffCredential(tok.AccessToken); err != nil {
			return fmt.Errorf("store credential: %w", err)
		}
		if err := writeKiffConfigValue("endpoint", base); err != nil {
			// Non-fatal: the token is stored; the endpoint can be re-supplied.
			fmt.Fprintf(out, "(note: could not persist endpoint to ~/.kiff/config: %v)\n", err)
		}
		fmt.Fprintf(out, "\nSigned in as %s (tenant %s) at %s\n", dash(tok.Subject), dash(tok.Tenant), base)
		return nil
	}
	return errors.New("device authorization expired before it was approved; run `kiff auth login` again")
}

func verificationTarget(s deviceStartResp) string {
	if strings.TrimSpace(s.VerificationURIComplete) != "" {
		return s.VerificationURIComplete
	}
	return s.VerificationURI
}

func deviceStart(client *http.Client, base string) (deviceStartResp, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/v1/auth/device/start", nil) // #nosec G107
	if err != nil {
		return deviceStartResp{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return deviceStartResp{}, fmt.Errorf("start device authorization: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return deviceStartResp{}, fmt.Errorf("start device authorization: server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out deviceStartResp
	if err := json.Unmarshal(body, &out); err != nil {
		return deviceStartResp{}, fmt.Errorf("decode start response: %w", err)
	}
	if out.DeviceCode == "" {
		return deviceStartResp{}, errors.New("start device authorization: empty device_code")
	}
	return out, nil
}

// pollDeviceToken makes one poll. It returns (token, "", nil) on
// success, ("", reason, nil) for the retryable pending/slow_down
// cases, and a terminal error otherwise (expired, denied, transport).
func pollDeviceToken(client *http.Client, base, deviceCode string) (deviceTokenResp, string, error) {
	payload, _ := json.Marshal(map[string]string{"device_code": deviceCode})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/v1/auth/device/token", bytes.NewReader(payload)) // #nosec G107
	if err != nil {
		return deviceTokenResp{}, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return deviceTokenResp{}, "", fmt.Errorf("poll device token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var tr deviceTokenResp
	_ = json.Unmarshal(body, &tr)
	if resp.StatusCode == http.StatusOK && tr.AccessToken != "" {
		return tr, "", nil
	}
	switch tr.Error {
	case "authorization_pending", "slow_down":
		return deviceTokenResp{}, tr.Error, nil
	case "expired_token":
		return deviceTokenResp{}, "", errors.New("the device code expired; run `kiff auth login` again")
	case "access_denied":
		return deviceTokenResp{}, "", errors.New("the sign-in request was denied")
	default:
		return deviceTokenResp{}, "", fmt.Errorf("poll device token: server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

// --- status ---

func authStatus(out io.Writer, args []string) error {
	fs := flag.NewFlagSet("auth status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	endpoint := fs.String("endpoint", "", "Base URL of the KIFF cloud")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	if err := fs.Parse(args); err != nil {
		authUsage()
		return helpOrErr(err)
	}
	// Check the credential before the endpoint: "not signed in" is the
	// more useful answer than "no endpoint" when neither is set.
	tok, err := resolveToken("")
	if err != nil {
		fmt.Fprintln(out, "not signed in — run `kiff auth login`")
		return nil
	}
	base, err := resolveEndpoint(*endpoint)
	if err != nil {
		fmt.Fprintln(out, "signed in, but no endpoint is configured — pass -endpoint or set KIFF_CLOUD_URL")
		return nil
	}
	client := &http.Client{Timeout: *timeout}
	var me struct {
		Subject    string `json:"subject"`
		TenantID   string `json:"tenant_id"`
		TenantSlug string `json:"tenant_slug"`
	}
	if err := cloudGet(client, base, tok, "/v1/me", &me); err != nil {
		fmt.Fprintf(out, "signed in, but the session was rejected (%v) — run `kiff auth login` again\n", err)
		return nil
	}
	tenant := me.TenantSlug
	if tenant == "" {
		tenant = me.TenantID
	}
	fmt.Fprintf(out, "signed in as %s (tenant %s) at %s\n", dash(me.Subject), dash(tenant), base)
	return nil
}

// --- logout ---

func authLogout(out io.Writer, args []string) error {
	fs := flag.NewFlagSet("auth logout", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	endpoint := fs.String("endpoint", "", "Base URL of the KIFF cloud")
	all := fs.Bool("all", false, "Revoke every session for this account (log out everywhere), not just this device")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	if err := fs.Parse(args); err != nil {
		authUsage()
		return helpOrErr(err)
	}
	tok, terr := resolveToken("")
	if terr != nil {
		fmt.Fprintln(out, "not signed in — nothing to do")
		return nil
	}
	// Best-effort server-side revoke; local forget happens regardless so
	// the credential never lingers on disk.
	if base, err := resolveEndpoint(*endpoint); err == nil {
		client := &http.Client{Timeout: *timeout}
		if err := deviceLogout(client, base, tok, *all); err != nil {
			fmt.Fprintf(out, "(note: server-side revoke failed: %v; forgetting the local credential anyway)\n", err)
		}
	}
	if err := forgetKiffCredential(); err != nil {
		return fmt.Errorf("forget credential: %w", err)
	}
	if *all {
		fmt.Fprintln(out, "signed out everywhere")
	} else {
		fmt.Fprintln(out, "signed out")
	}
	return nil
}

func deviceLogout(client *http.Client, base, token string, all bool) error {
	url := base + "/v1/auth/device/logout"
	if all {
		url += "?all=true"
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, nil) // #nosec G107
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("server returned %d", resp.StatusCode)
}

// --- credential storage (~/.kiff, 0600) ---

func writeKiffCredential(token string) error {
	dir := kiffHome()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "credentials"), []byte("token = "+token+"\n"), 0o600)
}

func forgetKiffCredential() error {
	err := os.Remove(filepath.Join(kiffHome(), "credentials"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// writeKiffConfigValue upserts a single key=value line in
// ~/.kiff/config, preserving other keys.
func writeKiffConfigValue(key, value string) error {
	dir := kiffHome()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "config")
	lines := []string{}
	replaced := false
	if existing, err := os.ReadFile(path); err == nil {
		// TrimRight the trailing newline so the split doesn't yield a
		// phantom empty element that would accumulate blank lines on
		// each rewrite; internal blank lines and comments are preserved.
		for _, line := range strings.Split(strings.TrimRight(string(existing), "\n"), "\n") {
			trimmed := strings.TrimSpace(line)
			if sep := strings.IndexAny(trimmed, "=:"); trimmed != "" && sep >= 0 && strings.EqualFold(strings.TrimSpace(trimmed[:sep]), key) {
				lines = append(lines, key+" = "+value)
				replaced = true
				continue
			}
			lines = append(lines, line) // preserve blank lines + comments
		}
	}
	if !replaced {
		lines = append(lines, key+" = "+value)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// openBrowser opens url in the platform browser (best-effort).
func openBrowser(url string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{url}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		name, args = "xdg-open", []string{url}
	}
	return exec.Command(name, args...).Start() // #nosec G204 — fixed opener, operator-supplied URL
}
