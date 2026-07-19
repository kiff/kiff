package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// operateServer stands up a fake KIFF cloud serving the Tier-1 GET
// routes with canned bodies, recording the paths hit and asserting
// every request carries the bearer.
type operateServer struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen []string
}

func newOperateServer(t *testing.T) *operateServer {
	t.Helper()
	os := &operateServer{}
	mux := http.NewServeMux()
	record := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			os.mu.Lock()
			os.seen = append(os.seen, r.URL.Path)
			os.mu.Unlock()
			h(w, r)
		}
	}
	mux.HandleFunc("GET /v1/me/domains", record(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"domains":[{"name":"prod-guardian","entity":"Server","version":3,"status":"enforce","action_count":4}]}`)
	}))
	mux.HandleFunc("GET /v1/me/domains/{domain}", record(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("domain") != "prod-guardian" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"name":"prod-guardian","entity":"Server","version":3,"status":"enforce",
			"actions":[{"name":"RESTART_SERVICE","risk":"high","approval":"never","allowed_states":["LIVE"],"executor":"ops.restart"}],
			"lifecycle":{"states":["LIVE","DOWN"],"events":["CRASHED"],"transitions":[{"on":"CRASHED","from":"LIVE","to":"DOWN"}]},
			"agents":{"status":"observed","agents":[{"id":"key-A","kind":"api_key","operations":5}]},
			"evidence":{"control_id":"ctrl-1"}}`)
	}))
	mux.HandleFunc("GET /v1/guard/runtimes", record(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"runtimes":[{"runtime_id":"rt-1","agent_id":"agent-x","adapter":"bedrock","mode":"enforce","project":"p","environment":"prod","seen_count":9}]}`)
	}))
	mux.HandleFunc("GET /v1/me/usage", record(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"plan":"beta","monthly_proposal_limit":1000,"counters":{"proposals":12}}`)
	}))
	mux.HandleFunc("GET /v1/me/domains/{domain}/usage", record(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"plan":"beta","monthly_proposal_limit":1000,"counters":{"proposals":3}}`)
	}))
	mux.HandleFunc("GET /v1/keys", record(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"keys":[{"id":"key-1","label":"loader","roles":["ops_agent"],"created_at":"2026-01-01T00:00:00Z"}]}`)
	}))
	os.srv = httptest.NewServer(mux)
	t.Cleanup(os.srv.Close)
	return os
}

func (o *operateServer) hit(path string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, p := range o.seen {
		if p == path {
			return true
		}
	}
	return false
}

func flags(url string, extra ...string) []string {
	return append([]string{"-endpoint", url, "-token", "tok"}, extra...)
}

func TestRunDomainsList(t *testing.T) {
	t.Parallel()
	s := newOperateServer(t)
	var buf bytes.Buffer
	if err := runDomains(&buf, append([]string{"list"}, flags(s.srv.URL)...)); err != nil {
		t.Fatalf("domains list: %v", err)
	}
	if !s.hit("/v1/me/domains") {
		t.Errorf("did not hit /v1/me/domains; seen=%v", s.seen)
	}
	if out := buf.String(); !strings.Contains(out, "prod-guardian") || !strings.Contains(out, "enforce") {
		t.Errorf("rendered table missing expected content:\n%s", out)
	}
}

func TestRunDomainsShow(t *testing.T) {
	t.Parallel()
	s := newOperateServer(t)
	var buf bytes.Buffer
	if err := runDomains(&buf, append([]string{"show", "prod-guardian"}, flags(s.srv.URL)...)); err != nil {
		t.Fatalf("domains show: %v", err)
	}
	if !s.hit("/v1/me/domains/prod-guardian") {
		t.Errorf("did not hit the single-domain route; seen=%v", s.seen)
	}
	out := buf.String()
	for _, want := range []string{"RESTART_SERVICE", "ops.restart", "CRASHED", "key-A", "ctrl-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}
}

func TestRunDomainsShow_RequiresName(t *testing.T) {
	t.Parallel()
	s := newOperateServer(t)
	var buf bytes.Buffer
	if err := runDomains(&buf, append([]string{"show"}, flags(s.srv.URL)...)); err == nil {
		t.Fatal("want error when no name is given")
	}
}

func TestRunRuntimes(t *testing.T) {
	t.Parallel()
	s := newOperateServer(t)
	var buf bytes.Buffer
	if err := runRuntimes(&buf, flags(s.srv.URL)); err != nil {
		t.Fatalf("runtimes: %v", err)
	}
	if out := buf.String(); !s.hit("/v1/guard/runtimes") || !strings.Contains(out, "rt-1") || !strings.Contains(out, "bedrock") {
		t.Errorf("runtimes output/paths wrong; out=%s seen=%v", out, s.seen)
	}
}

func TestRunUsage_TenantAndDomain(t *testing.T) {
	t.Parallel()
	s := newOperateServer(t)
	var buf bytes.Buffer
	if err := runUsage(&buf, flags(s.srv.URL)); err != nil {
		t.Fatalf("usage: %v", err)
	}
	if !s.hit("/v1/me/usage") {
		t.Errorf("tenant usage path not hit; seen=%v", s.seen)
	}
	buf.Reset()
	if err := runUsage(&buf, flags(s.srv.URL, "-domain", "prod-guardian")); err != nil {
		t.Fatalf("usage --domain: %v", err)
	}
	if !s.hit("/v1/me/domains/prod-guardian/usage") {
		t.Errorf("per-domain usage path not hit; seen=%v", s.seen)
	}
}

func TestRunKeysList(t *testing.T) {
	t.Parallel()
	s := newOperateServer(t)
	var buf bytes.Buffer
	if err := runKeys(&buf, append([]string{"list"}, flags(s.srv.URL)...)); err != nil {
		t.Fatalf("keys list: %v", err)
	}
	if out := buf.String(); !s.hit("/v1/keys") || !strings.Contains(out, "key-1") || !strings.Contains(out, "loader") {
		t.Errorf("keys output/paths wrong; out=%s seen=%v", out, s.seen)
	}
}

func TestRunKeys_MintIsNotTier1(t *testing.T) {
	t.Parallel()
	s := newOperateServer(t)
	var buf bytes.Buffer
	err := runKeys(&buf, append([]string{"create"}, flags(s.srv.URL)...))
	if err == nil || !strings.Contains(err.Error(), "Tier-2") {
		t.Fatalf("keys create should be rejected as a Tier-2 follow-up; got %v", err)
	}
}

func TestCloudGet_ForbiddenRenders(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"authoring a domain requires the tenant_owner role"}`)
	}))
	defer srv.Close()
	err := cloudGet(srv.Client(), srv.URL, "tok", "/v1/me/domains", &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "tenant_owner") {
		t.Fatalf("403 should surface the server message; got %v", err)
	}
}

func TestRenderHelpers(t *testing.T) {
	t.Parallel()
	if dash("") != "-" || dash("x") != "x" {
		t.Error("dash")
	}
	if states(nil) != "-" || states([]string{"A", "B"}) != "[A,B]" {
		t.Error("states")
	}
}
