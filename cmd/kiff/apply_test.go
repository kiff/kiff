package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSlugify_MatchesCloud pins the CLI's slug rule to the cloud's
// domain.Slugify, so the {slug} apply builds for the update URL
// resolves to the same stored domain.
func TestSlugify_MatchesCloud(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Refund Flow":      "refund-flow",
		"Vendor_Payments":  "vendor-payments",
		"refunds":          "refunds",
		"prod-guardian":    "prod-guardian",
		"  Spaced  Name  ": "spaced-name",
		"a/b.c":            "a-b-c",
		"!!!":              "",
		"Trailing---":      "trailing",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDomainNameFromYAML(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"plain", "domain: prod-guardian\nentity: Server\n", "prod-guardian"},
		{"quoted", "domain: \"Refund Flow\"\n", "Refund Flow"},
		{"single-quoted", "domain: 'refunds'\n", "refunds"},
		{"inline-comment", "domain: refunds # the payments domain\n", "refunds"},
		{"ignores-nested", "spec:\n  domain: not-this\ndomain: real\n", "real"},
		{"missing", "entity: Server\nstates: [NEW]\n", ""},
		{"leading-comment", "# a contract\ndomain: guardian\n", "guardian"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := domainNameFromYAML([]byte(tc.yaml)); got != tc.want {
				t.Errorf("domainNameFromYAML = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveEndpoint_Order(t *testing.T) {
	// Not parallel: mutates env.
	t.Setenv("KIFF_CLOUD_URL", "")
	t.Setenv("HOME", t.TempDir()) // no ~/.kiff/config
	defaultCloudEndpoint = ""     // OSS default: empty

	if _, err := resolveEndpoint(""); err == nil {
		t.Fatal("want error when no endpoint is set anywhere")
	}
	// Env is used when the flag is empty.
	t.Setenv("KIFF_CLOUD_URL", "https://api.example.com/")
	got, err := resolveEndpoint("")
	if err != nil {
		t.Fatalf("resolveEndpoint(env): %v", err)
	}
	if got != "https://api.example.com" { // trailing slash trimmed
		t.Errorf("env endpoint = %q", got)
	}
	// Flag wins over env.
	got, err = resolveEndpoint("http://localhost:8080")
	if err != nil {
		t.Fatalf("resolveEndpoint(flag): %v", err)
	}
	if got != "http://localhost:8080" {
		t.Errorf("flag endpoint = %q", got)
	}
	// A non-URL is rejected.
	if _, err := resolveEndpoint("not-a-url"); err == nil {
		t.Error("want error for a non-http(s) endpoint")
	}
}

func TestResolveToken_Order(t *testing.T) {
	t.Setenv("KIFF_TOKEN", "")
	t.Setenv("HOME", t.TempDir()) // no ~/.kiff/credentials

	if _, err := resolveToken(""); err == nil {
		t.Fatal("want error when no token is set anywhere")
	}
	t.Setenv("KIFF_TOKEN", "env-tok")
	if got, err := resolveToken(""); err != nil || got != "env-tok" {
		t.Fatalf("env token = %q, err = %v", got, err)
	}
	if got, err := resolveToken("flag-tok"); err != nil || got != "flag-tok" {
		t.Fatalf("flag token = %q, err = %v", got, err)
	}
}

func TestDomainExists(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/me/domains" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			// "Refund Flow" is a multi-word display name; the slug-based
			// match must still resolve it from a differently-separated
			// file name.
			"domains": []map[string]any{{"name": "prod-guardian"}, {"name": "Refund Flow"}},
		})
	}))
	defer srv.Close()

	client := srv.Client()
	if ok, err := domainExists(client, srv.URL, "tok", "Prod-Guardian"); err != nil || !ok {
		t.Fatalf("existing (case-insensitive): ok=%v err=%v", ok, err)
	}
	// Separator/case differences resolve via the derived slug:
	// "Refund_Flow" -> "refund-flow" == slug of listed "Refund Flow".
	if ok, err := domainExists(client, srv.URL, "tok", "Refund_Flow"); err != nil || !ok {
		t.Fatalf("slug match: ok=%v err=%v", ok, err)
	}
	if ok, err := domainExists(client, srv.URL, "tok", "new-domain"); err != nil || ok {
		t.Fatalf("absent: ok=%v err=%v", ok, err)
	}
	if _, err := domainExists(client, srv.URL, "bad", "x"); err == nil {
		t.Fatal("want error on 401")
	}
}

func TestSendContract_Success(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"domain": "prod-guardian", "entity": "Server", "version": 3,
		})
	}))
	defer srv.Close()

	got, err := sendContract(srv.Client(), http.MethodPost, srv.URL+"/v1/me/domains", "tok", []byte("domain: prod-guardian\n"))
	if err != nil {
		t.Fatalf("sendContract: %v", err)
	}
	if got.Domain != "prod-guardian" || got.Version != 3 {
		t.Errorf("result = %+v", got)
	}
}

func TestApplyHTTPError_Rendering(t *testing.T) {
	t.Parallel()
	// 422 with issues → each issue rendered.
	body := []byte(`{"error":"domain fails validation; cannot update","issues":[{"path":"actions[0].executor","message":"unknown executor"},{"message":"no states defined"}]}`)
	err := applyHTTPError(http.StatusUnprocessableEntity, body)
	if err == nil {
		t.Fatal("want error for 422")
	}
	msg := err.Error()
	for _, want := range []string{"cannot update", "actions[0].executor", "unknown executor", "no states defined"} {
		if !strings.Contains(msg, want) {
			t.Errorf("422 message missing %q: %s", want, msg)
		}
	}
	// 403 → surfaces the server's role message.
	err = applyHTTPError(http.StatusForbidden, []byte(`{"error":"authoring a domain requires the tenant_owner role"}`))
	if err == nil || !strings.Contains(err.Error(), "tenant_owner") {
		t.Errorf("403 message = %v", err)
	}
	// 401 → generic token-rejected hint.
	err = applyHTTPError(http.StatusUnauthorized, nil)
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("401 message = %v", err)
	}
}
