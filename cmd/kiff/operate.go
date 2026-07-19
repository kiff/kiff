package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

// operate.go — the Tier-1 read/operate commands (RFC 034 Decision 2).
//
// These wrap the cloud's existing GET routes so a developer (or their
// coding agent) can inspect a workspace from the terminal:
//
//	kiff domains list                list the tenant's governed domains
//	kiff domains show <name>         one domain: actions, lifecycle, agents
//	kiff runtimes                    connected runtimes (guard)
//	kiff usage [--domain <name>]     governed-operation counters
//	kiff keys list                   active API keys (never the secret)
//
// Tier 1 is read-only by construction: every command here issues a
// GET and never mutates. They share `apply`'s cloud-agnostic endpoint
// and token resolution (Decision 1) — nothing names our cloud in
// source — and degrade gracefully on any server error, including the
// 403 the management gates return.
//
// Each command takes an io.Writer for its output so it is testable
// against a buffer without touching the process-global os.Stdout;
// main passes os.Stdout.

// commonFlags are the endpoint/token/json/timeout flags every
// operate command shares.
type commonFlags struct {
	endpoint *string
	token    *string
	jsonOut  *bool
	timeout  *time.Duration
}

func addCommonFlags(fs *flag.FlagSet) commonFlags {
	return commonFlags{
		endpoint: fs.String("endpoint", "", "Base URL of the KIFF cloud (overrides KIFF_CLOUD_URL, ~/.kiff/config)"),
		token:    fs.String("token", "", "Bearer token (overrides KIFF_TOKEN, ~/.kiff/credentials)"),
		jsonOut:  fs.Bool("json", false, "Emit raw JSON instead of a table"),
		timeout:  fs.Duration("timeout", 30*time.Second, "HTTP timeout"),
	}
}

// resolve turns the parsed flags into a base URL, token, and client,
// applying the shared resolution order (reused from apply).
func (c commonFlags) resolve() (base, token string, client *http.Client, err error) {
	base, err = resolveEndpoint(*c.endpoint)
	if err != nil {
		return "", "", nil, err
	}
	token, err = resolveToken(*c.token)
	if err != nil {
		return "", "", nil, err
	}
	return base, token, &http.Client{Timeout: *c.timeout}, nil
}

// cloudGet issues an authenticated GET and decodes a 2xx body into
// out. A non-2xx is turned into a readable error (reusing apply's
// renderer, so 401/403 read the same across commands).
func cloudGet(client *http.Client, base, token, path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, base+path, nil) // #nosec G107 — endpoint is operator-supplied
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return applyHTTPError(resp.StatusCode, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// printJSON pretty-prints a value (for -json output).
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func newTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
}

// --- kiff domains list | show ---

func runDomains(out io.Writer, args []string) error {
	sub := "list"
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		rest = args[1:]
	}
	switch sub {
	case "list":
		return domainsList(out, rest)
	case "show":
		return domainsShow(out, rest)
	default:
		domainsUsage()
		return fmt.Errorf("unknown subcommand %q", sub)
	}
}

func domainsUsage() {
	fmt.Fprintln(os.Stderr, "kiff domains list                 List the tenant's governed domains")
	fmt.Fprintln(os.Stderr, "kiff domains show <name>          Show one domain's actions, lifecycle, agents")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags: -endpoint URL  -token TOKEN  -json")
}

type domainListItem struct {
	Name        string `json:"name"`
	Entity      string `json:"entity"`
	Version     int    `json:"version"`
	Status      string `json:"status"`
	ActionCount int    `json:"action_count"`
}

func domainsList(out io.Writer, args []string) error {
	fs := flag.NewFlagSet("domains list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cf := addCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		domainsUsage()
		return helpOrErr(err)
	}
	base, token, client, err := cf.resolve()
	if err != nil {
		return err
	}
	var body struct {
		Domains []domainListItem `json:"domains"`
	}
	if err := cloudGet(client, base, token, "/v1/me/domains", &body); err != nil {
		return err
	}
	if *cf.jsonOut {
		return printJSON(out, body)
	}
	if len(body.Domains) == 0 {
		fmt.Fprintln(out, "no domains")
		return nil
	}
	tw := newTabWriter(out)
	fmt.Fprintln(tw, "NAME\tENTITY\tVERSION\tSTATUS\tACTIONS")
	for _, d := range body.Domains {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%d\n", d.Name, d.Entity, d.Version, d.Status, d.ActionCount)
	}
	return tw.Flush()
}

type domainDetail struct {
	Name    string `json:"name"`
	Entity  string `json:"entity"`
	Version int    `json:"version"`
	Status  string `json:"status"`
	Actions []struct {
		Name          string   `json:"name"`
		Risk          string   `json:"risk"`
		Approval      string   `json:"approval"`
		AllowedStates []string `json:"allowed_states"`
		Executor      string   `json:"executor"`
	} `json:"actions"`
	Lifecycle struct {
		States      []string `json:"states"`
		Events      []string `json:"events"`
		Transitions []struct {
			On   string `json:"on"`
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"transitions"`
	} `json:"lifecycle"`
	Agents struct {
		Status string `json:"status"`
		Agents []struct {
			ID         string `json:"id"`
			Kind       string `json:"kind"`
			Operations int    `json:"operations"`
		} `json:"agents"`
	} `json:"agents"`
	Evidence struct {
		ControlID string `json:"control_id"`
	} `json:"evidence"`
}

func domainsShow(out io.Writer, args []string) error {
	// The domain name is the leading positional (kiff domains show
	// <name> [flags]). Pull it off the front before flag parsing —
	// Go's flag package stops at the first non-flag token, so a name
	// ahead of the flags would otherwise leave -endpoint/-token
	// unparsed.
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = strings.TrimSpace(args[0])
		args = args[1:]
	}
	fs := flag.NewFlagSet("domains show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cf := addCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		domainsUsage()
		return helpOrErr(err)
	}
	if name == "" {
		domainsUsage()
		return errors.New("domains show requires a <name>")
	}
	base, token, client, err := cf.resolve()
	if err != nil {
		return err
	}
	var d domainDetail
	if err := cloudGet(client, base, token, "/v1/me/domains/"+url.PathEscape(name), &d); err != nil {
		return err
	}
	if *cf.jsonOut {
		return printJSON(out, d)
	}
	fmt.Fprintf(out, "%s  (entity %s, version %d, status %s)\n", d.Name, d.Entity, d.Version, d.Status)
	fmt.Fprintln(out, "\nactions:")
	tw := newTabWriter(out)
	fmt.Fprintln(tw, "  NAME\tRISK\tAPPROVAL\tSTATES\tEXECUTOR")
	for _, a := range d.Actions {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", a.Name, dash(a.Risk), dash(a.Approval), states(a.AllowedStates), dash(a.Executor))
	}
	_ = tw.Flush()
	fmt.Fprintf(out, "\nlifecycle: states=%s events=%s\n", states(d.Lifecycle.States), states(d.Lifecycle.Events))
	for _, tr := range d.Lifecycle.Transitions {
		fmt.Fprintf(out, "  on %s: %s -> %s\n", tr.On, tr.From, tr.To)
	}
	fmt.Fprintf(out, "\nagents (%s):\n", d.Agents.Status)
	if len(d.Agents.Agents) == 0 {
		fmt.Fprintln(out, "  (none observed)")
	}
	for _, ag := range d.Agents.Agents {
		fmt.Fprintf(out, "  %s (%s) ops=%d\n", ag.ID, ag.Kind, ag.Operations)
	}
	if d.Evidence.ControlID != "" {
		fmt.Fprintf(out, "\nevidence control: %s\n", d.Evidence.ControlID)
	}
	return nil
}

// --- kiff runtimes ---

func runRuntimes(out io.Writer, args []string) error {
	fs := flag.NewFlagSet("runtimes", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cf := addCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		runtimesUsage()
		return helpOrErr(err)
	}
	base, token, client, err := cf.resolve()
	if err != nil {
		return err
	}
	var body struct {
		Runtimes []struct {
			RuntimeID   string    `json:"runtime_id"`
			AgentID     string    `json:"agent_id"`
			Adapter     string    `json:"adapter"`
			Mode        string    `json:"mode"`
			Project     string    `json:"project"`
			Environment string    `json:"environment"`
			LastSeenAt  time.Time `json:"last_seen_at"`
			SeenCount   uint64    `json:"seen_count"`
		} `json:"runtimes"`
	}
	if err := cloudGet(client, base, token, "/v1/guard/runtimes", &body); err != nil {
		return err
	}
	if *cf.jsonOut {
		return printJSON(out, body)
	}
	if len(body.Runtimes) == 0 {
		fmt.Fprintln(out, "no runtimes connected")
		return nil
	}
	tw := newTabWriter(out)
	fmt.Fprintln(tw, "RUNTIME\tAGENT\tADAPTER\tMODE\tPROJECT/ENV\tLAST SEEN\tSEEN")
	for _, rt := range body.Runtimes {
		penv := strings.TrimSuffix(dash(rt.Project)+"/"+dash(rt.Environment), "/")
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n",
			rt.RuntimeID, dash(rt.AgentID), dash(rt.Adapter), dash(rt.Mode), penv,
			rt.LastSeenAt.Format(time.RFC3339), rt.SeenCount)
	}
	return tw.Flush()
}

func runtimesUsage() {
	fmt.Fprintln(os.Stderr, "kiff runtimes [-endpoint URL] [-token TOKEN] [-json]")
	fmt.Fprintln(os.Stderr, "List the runtimes currently connected to the tenant (guard).")
}

// --- kiff usage ---

func runUsage(out io.Writer, args []string) error {
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cf := addCommonFlags(fs)
	domain := fs.String("domain", "", "Scope to one domain (defaults to the whole tenant)")
	if err := fs.Parse(args); err != nil {
		usageUsage()
		return helpOrErr(err)
	}
	base, token, client, err := cf.resolve()
	if err != nil {
		return err
	}
	path := "/v1/me/usage"
	if d := strings.TrimSpace(*domain); d != "" {
		path = "/v1/me/domains/" + url.PathEscape(d) + "/usage"
	}
	var body struct {
		Plan                 string            `json:"plan"`
		MonthlyProposalLimit int               `json:"monthly_proposal_limit"`
		From                 time.Time         `json:"from"`
		To                   time.Time         `json:"to"`
		Counters             map[string]uint64 `json:"counters"`
	}
	if err := cloudGet(client, base, token, path, &body); err != nil {
		return err
	}
	if *cf.jsonOut {
		return printJSON(out, body)
	}
	limit := "unlimited"
	if body.MonthlyProposalLimit > 0 {
		limit = fmt.Sprintf("%d", body.MonthlyProposalLimit)
	}
	scope := "tenant"
	if d := strings.TrimSpace(*domain); d != "" {
		scope = "domain " + d
	}
	fmt.Fprintf(out, "usage (%s) plan=%s limit=%s\n", scope, dash(body.Plan), limit)
	fmt.Fprintf(out, "window: %s .. %s\n", body.From.Format(time.RFC3339), body.To.Format(time.RFC3339))
	if len(body.Counters) == 0 {
		fmt.Fprintln(out, "counters: (none)")
		return nil
	}
	tw := newTabWriter(out)
	fmt.Fprintln(tw, "COUNTER\tVALUE")
	for k, v := range body.Counters {
		fmt.Fprintf(tw, "%s\t%d\n", k, v)
	}
	return tw.Flush()
}

func usageUsage() {
	fmt.Fprintln(os.Stderr, "kiff usage [--domain <name>] [-endpoint URL] [-token TOKEN] [-json]")
	fmt.Fprintln(os.Stderr, "Show governed-operation counters for the tenant, or one domain.")
}

// --- kiff keys list ---

func runKeys(out io.Writer, args []string) error {
	sub := "list"
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		rest = args[1:]
	}
	if sub != "list" {
		keysUsage()
		return fmt.Errorf("unknown or unsupported subcommand %q (only `list` is available; mint/revoke are a Tier-2 follow-up)", sub)
	}
	fs := flag.NewFlagSet("keys list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cf := addCommonFlags(fs)
	if err := fs.Parse(rest); err != nil {
		keysUsage()
		return helpOrErr(err)
	}
	base, token, client, err := cf.resolve()
	if err != nil {
		return err
	}
	var body struct {
		Keys []struct {
			ID         string     `json:"id"`
			Label      string     `json:"label"`
			Roles      []string   `json:"roles"`
			CreatedAt  time.Time  `json:"created_at"`
			LastUsedAt *time.Time `json:"last_used_at"`
		} `json:"keys"`
	}
	if err := cloudGet(client, base, token, "/v1/keys", &body); err != nil {
		return err
	}
	if *cf.jsonOut {
		return printJSON(out, body)
	}
	if len(body.Keys) == 0 {
		fmt.Fprintln(out, "no active keys")
		return nil
	}
	tw := newTabWriter(out)
	fmt.Fprintln(tw, "ID\tLABEL\tROLES\tCREATED\tLAST USED")
	for _, k := range body.Keys {
		last := "never"
		if k.LastUsedAt != nil {
			last = k.LastUsedAt.Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			k.ID, dash(k.Label), states(k.Roles), k.CreatedAt.Format(time.RFC3339), last)
	}
	return tw.Flush()
}

func keysUsage() {
	fmt.Fprintln(os.Stderr, "kiff keys list [-endpoint URL] [-token TOKEN] [-json]")
	fmt.Fprintln(os.Stderr, "List the tenant's active API keys (never the secret).")
}

// --- small shared render helpers ---

// helpOrErr converts flag.ErrHelp into a clean nil (exit 0), leaving
// other parse errors as-is.
func helpOrErr(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func states(ss []string) string {
	if len(ss) == 0 {
		return "-"
	}
	return "[" + strings.Join(ss, ",") + "]"
}
