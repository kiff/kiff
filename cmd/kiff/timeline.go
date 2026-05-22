package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// runTimeline implements the `kiff timeline` subcommand.
//
// It calls a running KIFF httpapi server's GET /entities/{id}/timeline
// route and renders a compact, human-readable table:
//
//	time | kind | actor | summary [target]
//
// It optionally calls GET /demo/rebuild?entity=<id> (if the server
// exposes it, like the refund-agno and support-ops demos do) and
// appends a final equality line confirming materialized == replayed.
//
// The subcommand is intentionally tiny and stdlib-only. It is meant
// for two audiences: demo-time explainers ("what happened on this
// entity?") and developers smoke-testing a new domain.
func runTimeline(args []string) error {
	fs := flag.NewFlagSet("timeline", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // we print our own usage on error
	base := fs.String("base", "http://localhost:8080", "Base URL of the KIFF httpapi server")
	entity := fs.String("entity", "", "Entity ID to render the timeline for")
	jsonOut := fs.Bool("json", false, "Emit raw JSON instead of the table")
	if err := fs.Parse(args); err != nil {
		timelineUsage()
		return err
	}
	if *entity == "" {
		timelineUsage()
		return errors.New("-entity is required")
	}
	baseURL := strings.TrimRight(*base, "/")
	timeline, err := fetchTimeline(baseURL, *entity)
	if err != nil {
		return fmt.Errorf("fetch timeline: %w", err)
	}
	rebuild, _ := fetchRebuild(baseURL, *entity) // best-effort

	if *jsonOut {
		out := map[string]any{
			"timeline": timeline,
		}
		if rebuild != nil {
			out["rebuild"] = rebuild
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	renderTable(*entity, timeline, rebuild)
	return nil
}

func timelineUsage() {
	fmt.Fprintln(os.Stderr, "kiff timeline -entity <id> [-base http://host:port] [-json]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Render the audit timeline for one entity from a running KIFF httpapi server.")
	fmt.Fprintln(os.Stderr, "If the server exposes /demo/rebuild, the rebuild result is appended.")
}

type timelineRecord struct {
	ID            string         `json:"id"`
	Kind          string         `json:"kind"`
	EntityID      string         `json:"entity_id"`
	EntityType    string         `json:"entity_type"`
	ActorID       string         `json:"actor_id"`
	Message       string         `json:"message"`
	Data          map[string]any `json:"data"`
	TraceID       string         `json:"trace_id,omitempty"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

func fetchTimeline(baseURL, entityID string) ([]timelineRecord, error) {
	url := baseURL + "/entities/" + entityID + "/timeline"
	resp, err := http.Get(url) // #nosec G107 — base URL is operator-supplied
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}
	var payload struct {
		Timeline []timelineRecord `json:"timeline"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return payload.Timeline, nil
}

type rebuildInfo struct {
	EntityID       string `json:"entity_id"`
	Materialized   string `json:"materialized"`
	Replayed       string `json:"replayed"`
	EventsReplayed int    `json:"events_replayed"`
	Matches        bool   `json:"matches"`
}

func fetchRebuild(baseURL, entityID string) (*rebuildInfo, error) {
	url := baseURL + "/demo/rebuild?entity=" + entityID
	resp, err := http.Get(url) // #nosec G107
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rebuild endpoint not available")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var info rebuildInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func renderTable(entity string, records []timelineRecord, rebuild *rebuildInfo) {
	fmt.Printf("timeline for %s (%d records)\n", entity, len(records))
	if len(records) == 0 {
		fmt.Println("  (no records)")
		return
	}
	fmt.Printf("  %-12s %-22s %-16s %s\n", "time", "kind", "actor", "summary")
	fmt.Println("  " + strings.Repeat("-", 80))
	for _, r := range records {
		t := r.CreatedAt.Format("15:04:05.000")
		message := r.Message
		if target := summaryTarget(r); target != "" {
			message = strings.TrimSpace(message) + " [" + target + "]"
		}
		if len(message) > 60 {
			message = message[:57] + "..."
		}
		fmt.Printf("  %-12s %-22s %-16s %s\n", t, r.Kind, truncate(r.ActorID, 16), message)
	}
	if rebuild != nil {
		marker := "✓"
		if !rebuild.Matches {
			marker = "✗"
		}
		fmt.Println()
		fmt.Printf("  rebuild: materialized=%q replayed=%q events=%d %s\n",
			rebuild.Materialized, rebuild.Replayed, rebuild.EventsReplayed, marker)
	}
}

// summaryTarget extracts a single short label from the audit data —
// either the action name (for action_* records) or the event type
// (for event_ingested / state_changed records). It is purely cosmetic
// and falls back to "" when nothing useful is present.
func summaryTarget(r timelineRecord) string {
	if r.Data == nil {
		return ""
	}
	if action, ok := r.Data["action"].(string); ok && action != "" {
		return action
	}
	if eventType, ok := r.Data["event_type"].(string); ok && eventType != "" {
		return eventType
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
