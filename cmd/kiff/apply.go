package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// defaultCloudEndpoint is the KIFF cloud the CLI targets when no
// -endpoint flag, KIFF_CLOUD_URL env, or ~/.kiff/config value is set.
//
// It is intentionally EMPTY in the open-source source tree so
// kiff/kiff never names a specific hosted instance (RFC 034
// decision 1, the framework-purity constraint). A distributor sets
// it at build time with:
//
//	go build -ldflags "-X main.defaultCloudEndpoint=https://api.example.com" ./cmd/kiff
//
// so a packaged build can point at a hosted KIFF without the URL
// living in source.
var defaultCloudEndpoint = ""

// runApply implements the `kiff apply` subcommand.
//
// It reads a local kiff.yaml (the git-versioned domain contract),
// resolves the target KIFF cloud endpoint and a bearer token, then
// pushes the contract to the tenant: PUT /v1/me/domains/{slug} when a
// domain of that name already exists, POST /v1/me/domains for the
// first apply. This is the write path of the "docker-compose for
// governed domains" model — the developer edits kiff.yaml in their
// repo (with their AI agent) and `kiff apply` makes the running
// version match, the same way `docker compose up` reconciles to a
// compose file.
//
// The command is cloud-agnostic (RFC 034 decision 1): it applies to
// *a* KIFF cloud identified by a configurable endpoint, never a
// hardcoded host. The cloud validates the contract server-side and
// returns structured issues, which this command renders.
//
// Interim auth: the bearer is a KIFF credential resolved from
// -token, KIFF_TOKEN, or ~/.kiff/credentials. The `kiff auth login`
// developer session (RFC 034-B) will populate the same slot; apply
// does not need to change when it lands.
func runApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // we print our own usage on error
	file := fs.String("f", "kiff.yaml", "Path to the domain contract (kiff.yaml)")
	endpoint := fs.String("endpoint", "", "Base URL of the KIFF cloud (overrides KIFF_CLOUD_URL, ~/.kiff/config)")
	token := fs.String("token", "", "Bearer token (overrides KIFF_TOKEN, ~/.kiff/credentials)")
	domainOverride := fs.String("domain", "", "Domain name to apply as (defaults to the `domain:` field in the file)")
	dryRun := fs.Bool("dry-run", false, "Resolve and print the plan without writing")
	jsonOut := fs.Bool("json", false, "Emit the result as JSON")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	if err := fs.Parse(args); err != nil {
		applyUsage()
		return err
	}

	yamlBytes, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read %s: %w", *file, err)
	}
	if len(bytes.TrimSpace(yamlBytes)) == 0 {
		return fmt.Errorf("%s is empty", *file)
	}

	name := strings.TrimSpace(*domainOverride)
	if name == "" {
		name = domainNameFromYAML(yamlBytes)
	}
	if name == "" {
		return errors.New("no domain name: the file has no top-level `domain:` field and -domain was not given")
	}
	slug := slugify(name)
	if slug == "" {
		return fmt.Errorf("domain name %q has no URL-safe form; pass an explicit -domain", name)
	}

	base, err := resolveEndpoint(*endpoint)
	if err != nil {
		return err
	}

	if *dryRun {
		return applyDryRun(base, *file, name, slug, *token, *timeout, *jsonOut)
	}

	tok, err := resolveToken(*token)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: *timeout}
	exists, err := domainExists(client, base, tok, name)
	if err != nil {
		return err
	}

	var (
		method string
		reqURL string
	)
	if exists {
		method = http.MethodPut
		reqURL = base + "/v1/me/domains/" + url.PathEscape(slug)
	} else {
		method = http.MethodPost
		reqURL = base + "/v1/me/domains"
	}

	summary, err := sendContract(client, method, reqURL, tok, yamlBytes)
	if err != nil {
		return err
	}

	action := "created"
	if exists {
		action = "updated"
	}
	if *jsonOut {
		out := map[string]any{
			"action":   action,
			"endpoint": base,
			"domain":   summary.Domain,
			"version":  summary.Version,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	fmt.Printf("%s %s (version %d) at %s\n", action, summary.Domain, summary.Version, base)
	return nil
}

func applyUsage() {
	fmt.Fprintln(os.Stderr, "kiff apply [-f kiff.yaml] [-endpoint URL] [-token TOKEN] [-domain NAME] [-dry-run] [-json]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Push a local kiff.yaml to a KIFF cloud so the running domain matches the file.")
	fmt.Fprintln(os.Stderr, "Existing domain -> PUT /v1/me/domains/{slug}; first apply -> POST /v1/me/domains.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "ENDPOINT resolution: -endpoint > KIFF_CLOUD_URL > ~/.kiff/config > build default.")
	fmt.Fprintln(os.Stderr, "TOKEN resolution:    -token > KIFF_TOKEN > ~/.kiff/credentials.")
}

// applyResult is the subset of the cloud's create/update success body
// this command renders.
type applyResult struct {
	Domain  string `json:"domain"`
	Entity  string `json:"entity"`
	Version int    `json:"version"`
}

// applyIssue mirrors one entry of the cloud's 422 validation issues.
type applyIssue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type applyErrorBody struct {
	Error  string       `json:"error"`
	Issues []applyIssue `json:"issues"`
}

// sendContract PUTs or POSTs the contract and decodes the result,
// turning a non-2xx into a readable error (including per-field
// validation issues on a 422).
func sendContract(client *http.Client, method, reqURL, token string, body []byte) (applyResult, error) {
	req, err := http.NewRequest(method, reqURL, bytes.NewReader(body)) // #nosec G107 — endpoint is operator-supplied
	if err != nil {
		return applyResult{}, err
	}
	req.Header.Set("Content-Type", "application/x-yaml")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return applyResult{}, fmt.Errorf("%s %s: %w", method, reqURL, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var out applyResult
		if err := json.Unmarshal(respBody, &out); err != nil {
			return applyResult{}, fmt.Errorf("decode response: %w", err)
		}
		return out, nil
	}
	return applyResult{}, applyHTTPError(resp.StatusCode, respBody)
}

// applyHTTPError renders a non-2xx into a helpful message.
func applyHTTPError(status int, body []byte) error {
	switch status {
	case http.StatusUnauthorized:
		return errors.New("unauthorized: the token was rejected (check -token / KIFF_TOKEN)")
	case http.StatusForbidden:
		// The tenant_owner gate: a plain runtime key cannot author.
		var e applyErrorBody
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return fmt.Errorf("forbidden: %s", e.Error)
		}
		return errors.New("forbidden: this token may not author domains (needs the tenant_owner role)")
	case http.StatusUnprocessableEntity:
		var e applyErrorBody
		if json.Unmarshal(body, &e) == nil {
			var b strings.Builder
			msg := e.Error
			if msg == "" {
				msg = "domain failed validation"
			}
			fmt.Fprintf(&b, "%s:", msg)
			for _, iss := range e.Issues {
				if iss.Path != "" {
					fmt.Fprintf(&b, "\n  - %s: %s", iss.Path, iss.Message)
				} else {
					fmt.Fprintf(&b, "\n  - %s", iss.Message)
				}
			}
			return errors.New(b.String())
		}
	}
	// Fallback: surface the raw error field or body.
	var e applyErrorBody
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return fmt.Errorf("server returned %d: %s", status, e.Error)
	}
	return fmt.Errorf("server returned %d: %s", status, strings.TrimSpace(string(body)))
}

// domainExists lists the tenant's domains and reports whether one of
// the given name (case-insensitive) is already present, so apply can
// choose PUT (update in place) over POST (create).
func domainExists(client *http.Client, base, token, name string) (bool, error) {
	req, err := http.NewRequest(http.MethodGet, base+"/v1/me/domains", nil) // #nosec G107
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("list domains: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return false, applyHTTPError(resp.StatusCode, body)
	}
	var payload struct {
		Domains []struct {
			Name string `json:"name"`
		} `json:"domains"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false, fmt.Errorf("decode domains list: %w", err)
	}
	for _, d := range payload.Domains {
		if strings.EqualFold(strings.TrimSpace(d.Name), name) {
			return true, nil
		}
	}
	return false, nil
}

// applyDryRun prints the resolved plan without writing. When a token
// is available it consults the tenant to say create vs update;
// otherwise it reports the endpoint/domain it would target.
func applyDryRun(base, file, name, slug, tokenFlag string, timeout time.Duration, jsonOut bool) error {
	plan := map[string]any{
		"dry_run":  true,
		"endpoint": base,
		"file":     file,
		"domain":   name,
		"slug":     slug,
	}
	action := "unknown (no token; existence not checked)"
	if tok, err := resolveToken(tokenFlag); err == nil {
		client := &http.Client{Timeout: timeout}
		exists, derr := domainExists(client, base, tok, name)
		if derr != nil {
			return derr
		}
		if exists {
			action = "update"
			plan["target"] = base + "/v1/me/domains/" + slug + " (PUT)"
		} else {
			action = "create"
			plan["target"] = base + "/v1/me/domains (POST)"
		}
	}
	plan["action"] = action

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(plan)
	}
	fmt.Printf("plan: %s %q at %s\n", action, name, base)
	fmt.Printf("  file: %s\n", file)
	fmt.Printf("  slug: %s\n", slug)
	if t, ok := plan["target"]; ok {
		fmt.Printf("  target: %s\n", t)
	}
	return nil
}

// resolveEndpoint applies the RFC 034 resolution order:
// flag > KIFF_CLOUD_URL > ~/.kiff/config > build-time default.
func resolveEndpoint(flagVal string) (string, error) {
	candidate := strings.TrimSpace(flagVal)
	if candidate == "" {
		candidate = strings.TrimSpace(os.Getenv("KIFF_CLOUD_URL"))
	}
	if candidate == "" {
		candidate = kiffConfigValue("endpoint")
	}
	if candidate == "" {
		candidate = strings.TrimSpace(defaultCloudEndpoint)
	}
	if candidate == "" {
		return "", errors.New("no endpoint: set -endpoint, KIFF_CLOUD_URL, or endpoint in ~/.kiff/config")
	}
	u, err := url.Parse(candidate)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("invalid endpoint %q: want an http(s) URL", candidate)
	}
	return strings.TrimRight(candidate, "/"), nil
}

// resolveToken applies the order flag > KIFF_TOKEN >
// ~/.kiff/credentials, returning an error when none is set.
func resolveToken(flagVal string) (string, error) {
	if t := strings.TrimSpace(flagVal); t != "" {
		return t, nil
	}
	if t := strings.TrimSpace(os.Getenv("KIFF_TOKEN")); t != "" {
		return t, nil
	}
	if t := kiffCredentialsToken(); t != "" {
		return t, nil
	}
	return "", errors.New("no token: set -token, KIFF_TOKEN, or a token in ~/.kiff/credentials")
}

// kiffConfigValue reads a `key = value` (or `key: value`) pair from
// ~/.kiff/config, returning "" when the file or key is absent.
// Lenient by design: config problems degrade to "unset", not errors.
func kiffConfigValue(key string) string {
	return keyedFileValue(filepath.Join(kiffHome(), "config"), key)
}

// kiffCredentialsToken reads the bearer from ~/.kiff/credentials. It
// accepts a `token = ...` / `token: ...` pair or, failing that, the
// first non-empty, non-comment line (so a file holding just the token
// works). Returns "" when the file is absent.
func kiffCredentialsToken() string {
	path := filepath.Join(kiffHome(), "credentials")
	if v := keyedFileValue(path, "token"); v != "" {
		return v
	}
	f, err := os.Open(path) // #nosec G304 — fixed path under the user's home
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.ContainsAny(line, "=:") {
			return line
		}
	}
	return ""
}

// keyedFileValue returns the value for key in a simple line-oriented
// `key = value` / `key: value` file, or "" if the file or key is
// missing. Comments (#) and blank lines are ignored.
func keyedFileValue(path, key string) string {
	f, err := os.Open(path) // #nosec G304 — fixed path under the user's home
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sep := strings.IndexAny(line, "=:")
		if sep < 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(line[:sep]), key) {
			return strings.Trim(strings.TrimSpace(line[sep+1:]), `"'`)
		}
	}
	return ""
}

// kiffHome is the ~/.kiff directory. Falls back to ".kiff" in the
// working directory when the home dir cannot be resolved.
func kiffHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".kiff"
	}
	return filepath.Join(home, ".kiff")
}

// domainNameFromYAML extracts the top-level `domain:` scalar from a
// kiff.yaml without a YAML dependency (the framework stays stdlib +
// pgx). It scans for an unindented `domain:` key and returns its
// unquoted value, or "" when absent. This mirrors the cloud's lenient
// DomainNameFromYAML for the common canonical layout.
func domainNameFromYAML(b []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		raw := sc.Text()
		// Only top-level keys (no leading whitespace) count, so a
		// nested `domain:` under some other block is not mistaken for
		// the contract's name.
		if len(raw) > 0 && (raw[0] == ' ' || raw[0] == '\t') {
			continue
		}
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "domain:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "domain:"))
			// Drop an inline comment, then surrounding quotes.
			if i := strings.Index(v, " #"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}

// slugify ports the cloud's domain.Slugify: lowercase, collapse runs
// of space/underscore/slash/dot/hyphen to a single hyphen, drop other
// characters, trim trailing hyphens. Kept in sync so the {slug} the
// CLI builds for the update URL matches the cloud's storage slug.
func slugify(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	b.Grow(len(lower))
	lastHyphen := false
	for _, r := range lower {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		case r == ' ' || r == '_' || r == '-' || r == '/' || r == '.':
			if b.Len() > 0 && !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		default:
			// drop
		}
	}
	return strings.TrimRight(b.String(), "-")
}
