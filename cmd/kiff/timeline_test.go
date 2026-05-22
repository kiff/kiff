package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestFetchTimeline_RoundTrip stands up a tiny test server that mimics
// the kiff httpapi /timeline route and verifies the client decodes
// the response into typed records.
func TestFetchTimeline_RoundTrip(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/timeline") {
			http.NotFound(w, r)
			return
		}
		body := map[string]any{
			"timeline": []map[string]any{
				{
					"id":         "audit-1",
					"kind":       "event_ingested",
					"entity_id":  "order-1",
					"actor_id":   "system",
					"message":    "event ingested",
					"data":       map[string]any{"event_type": "ORDER_PLACED"},
					"created_at": time.Now().UTC(),
				},
				{
					"id":         "audit-2",
					"kind":       "action_executed",
					"entity_id":  "order-1",
					"actor_id":   "support-agent",
					"message":    "action executed",
					"data":       map[string]any{"action": "AUTO_REFUND"},
					"created_at": time.Now().UTC(),
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	records, err := fetchTimeline(srv.URL, "order-1")
	if err != nil {
		t.Fatalf("fetchTimeline: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].Kind != "event_ingested" {
		t.Fatalf("expected event_ingested, got %s", records[0].Kind)
	}
	if got := summaryTarget(records[1]); got != "AUTO_REFUND" {
		t.Fatalf("expected AUTO_REFUND, got %q", got)
	}
}

// TestFetchRebuild_OK verifies the rebuild client decodes the demo
// server's /demo/rebuild response.
func TestFetchRebuild_OK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"entity_id":       "order-1",
			"materialized":    "REFUNDED",
			"replayed":        "REFUNDED",
			"events_replayed": 3,
			"matches":         true,
		})
	}))
	defer srv.Close()

	info, err := fetchRebuild(srv.URL, "order-1")
	if err != nil {
		t.Fatalf("fetchRebuild: %v", err)
	}
	if !info.Matches {
		t.Fatalf("expected matches=true, got %+v", info)
	}
}

// TestFetchRebuild_NotFound covers a server that does not expose the
// rebuild route — the timeline subcommand still works without it.
func TestFetchRebuild_NotFound(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	if _, err := fetchRebuild(srv.URL, "order-1"); err == nil {
		t.Fatalf("expected error when rebuild route is missing")
	}
}

// TestSummaryTarget_Fallback covers records that have no useful target
// fields; the helper returns empty string.
func TestSummaryTarget_Fallback(t *testing.T) {
	t.Parallel()
	if got := summaryTarget(timelineRecord{}); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := summaryTarget(timelineRecord{Data: map[string]any{"unrelated": 7}}); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

// TestTruncate covers the small string truncation helper used in the
// table renderer.
func TestTruncate(t *testing.T) {
	t.Parallel()
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("got %q", got)
	}
	if got := truncate("a much longer string", 10); got != "a much ..." {
		t.Fatalf("got %q", got)
	}
}
