package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	payables "github.com/kiff/kiff/cookbook/accounts-payable-payout"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8790", "listen address")
	region := flag.String("region", envFirst("AWS_REGION", "AWS_DEFAULT_REGION", "us-east-1"), "AWS region")
	model := flag.String("model", envFirst("KIFF_BEDROCK_MODEL", "", "us.anthropic.claude-haiku-4-5-20251001-v1:0"), "Bedrock model ID")
	flag.Parse()

	agent := payables.NewBedrockAgent(*region, *model)
	app, err := payables.NewInteractiveApp(agent, fmt.Sprintf("bedrock:%s", *model))
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(indexHTML))
	})
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		snapshot, err := app.Snapshot(r.Context())
		writeJSON(w, snapshot, err)
	})
	mux.HandleFunc("/api/input", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		snapshot, err := app.ProcessInput(ctx, body.Text)
		writeJSON(w, snapshot, err)
	})
	mux.HandleFunc("/api/approval", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Granted bool `json:"granted"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		snapshot, err := app.ReviewHeld(r.Context(), body.Granted)
		writeJSON(w, snapshot, err)
	})
	mux.HandleFunc("/api/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		snapshot, err := app.Reset(r.Context())
		writeJSON(w, snapshot, err)
	})

	log.Printf("KIFF AP payout UI listening on http://%s", *addr)
	log.Printf("Bedrock model: %s (%s)", *model, *region)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

func writeJSON(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func envFirst(names ...string) string {
	for _, name := range names {
		if name == "" {
			continue
		}
		if strings.Contains(name, ":") || strings.Contains(name, ".") || strings.Contains(name, "-") {
			return name
		}
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

const indexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>KIFF AP Payout</title>
  <style>
    :root {
      color-scheme: light;
      --ink: #1d252b;
      --muted: #65727b;
      --line: #cfd8de;
      --panel: #ffffff;
      --bg: #f5f7f8;
      --blue: #1e5d7d;
      --green: #24734d;
      --amber: #8b6400;
      --red: #a83d34;
      --steel: #56636c;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      color: var(--ink);
      background: var(--bg);
    }
    header {
      height: 56px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 0 20px;
      border-bottom: 1px solid var(--line);
      background: #fff;
    }
    h1 { margin: 0; font-size: 18px; font-weight: 700; letter-spacing: 0; }
    main {
      display: grid;
      grid-template-columns: minmax(310px, 430px) minmax(380px, 1fr) minmax(330px, 460px);
      gap: 14px;
      padding: 14px;
      min-height: calc(100vh - 56px);
    }
    section {
      min-width: 0;
      overflow: hidden;
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
    }
    .section-head {
      min-height: 42px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 8px;
      padding: 10px 12px;
      border-bottom: 1px solid var(--line);
      font-size: 13px;
      font-weight: 700;
      color: #2c363c;
    }
    .content { padding: 12px; }
    textarea {
      width: 100%;
      min-height: 145px;
      resize: vertical;
      border: 1px solid var(--line);
      border-radius: 6px;
      padding: 10px;
      font: inherit;
      line-height: 1.35;
      background: #fbfcfd;
      color: var(--ink);
    }
    button {
      border: 1px solid #aeb8c0;
      background: #fff;
      color: var(--ink);
      border-radius: 6px;
      padding: 8px 10px;
      font: inherit;
      font-size: 13px;
      cursor: pointer;
    }
    button.primary { background: var(--blue); color: #fff; border-color: var(--blue); }
    button.danger { color: var(--red); border-color: var(--red); }
    button:disabled { opacity: .55; cursor: wait; }
    .button-row { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
    .status-grid {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 8px;
      margin-bottom: 10px;
    }
    .metric {
      min-height: 66px;
      border: 1px solid var(--line);
      border-radius: 6px;
      padding: 9px;
      background: #fbfcfd;
    }
    .metric span { display: block; font-size: 11px; color: var(--muted); margin-bottom: 5px; }
    .metric strong { display: block; font-size: 14px; line-height: 1.2; overflow-wrap: anywhere; }
    .pill-row { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 10px; }
    .pill {
      display: inline-flex;
      align-items: center;
      min-height: 24px;
      border: 1px solid var(--line);
      border-radius: 999px;
      padding: 3px 8px;
      background: #f9fafb;
      color: #334047;
      font-size: 12px;
    }
    .proposal, .held, .payments {
      border: 1px solid var(--line);
      border-radius: 6px;
      padding: 10px;
      background: #fbfcfd;
      margin-bottom: 10px;
    }
    .held { border-color: #d6b15d; background: #fff8e7; color: #5d4200; }
    .payments { border-color: #bad2c4; background: #f2faf5; color: #214e36; }
    .proposal h2, .held h2, .payments h2 { margin: 0 0 6px; font-size: 15px; letter-spacing: 0; }
    .proposal p, .held p, .payments p { margin: 0; color: var(--muted); font-size: 13px; line-height: 1.4; }
    .facts {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 6px;
      margin-bottom: 10px;
    }
    .fact {
      border: 1px solid var(--line);
      border-radius: 6px;
      padding: 7px;
      font-size: 12px;
      background: #fff;
    }
    .fact span { display: block; color: var(--muted); font-size: 10px; margin-bottom: 3px; }
    .fact strong { overflow-wrap: anywhere; }
    .log, .timeline {
      display: flex;
      flex-direction: column;
      gap: 8px;
      max-height: calc(100vh - 300px);
      overflow: auto;
      padding-right: 2px;
    }
    .line, .event {
      border-left: 3px solid var(--steel);
      padding: 4px 0 4px 8px;
      font-size: 13px;
      line-height: 1.35;
    }
    .line small, .event small { display: block; color: var(--muted); font-size: 11px; margin-bottom: 2px; }
    .kind-agent { border-left-color: var(--blue); }
    .kind-kiff { border-left-color: var(--green); }
    .kind-human { border-left-color: var(--amber); }
    .kind-input { border-left-color: var(--steel); }
    .empty { color: var(--muted); font-size: 13px; padding: 8px 0; }
    @media (max-width: 1120px) {
      main { grid-template-columns: 1fr; }
      .log, .timeline { max-height: none; }
    }
  </style>
</head>
<body>
  <header>
    <h1>Accounts Payable Payout</h1>
    <button id="reset" class="danger">Reset</button>
  </header>
  <main>
    <section>
      <div class="section-head">Operator Input</div>
      <div class="content">
        <textarea id="input">Invoice INV-7741 from Northwind Parts for $18,420.00 USD, vendor vendor-northwind, bank ACH-9912, due 2026-07-15.</textarea>
        <div class="button-row">
          <button class="primary" id="submit">Submit</button>
          <button data-example="Invoice INV-7741 from Northwind Parts for $18,420.00 USD, vendor vendor-northwind, bank ACH-9912, due 2026-07-15.">Invoice</button>
          <button data-example="Mark the verified invoice ready for payment.">Ready</button>
          <button data-example="Pay this invoice today.">Pay</button>
          <button data-example="Pay this invoice again.">Duplicate</button>
        </div>
      </div>
    </section>

    <section>
      <div class="section-head">
        <span>Payment Control</span>
        <span id="model" class="pill">bedrock</span>
      </div>
      <div class="content">
        <div class="status-grid">
          <div class="metric"><span>Invoice</span><strong id="invoice">none</strong></div>
          <div class="metric"><span>State</span><strong id="state">none</strong></div>
        </div>
        <div class="facts" id="facts"></div>
        <div class="proposal">
          <h2 id="proposal-action">No proposal</h2>
          <p id="proposal-reason">Waiting for input.</p>
        </div>
        <div id="allowed" class="pill-row"></div>
        <div id="held"></div>
        <div id="payments"></div>
        <div class="section-head" style="padding-left:0;border-bottom:0;margin-top:12px;">Run Log</div>
        <div id="lines" class="log"></div>
      </div>
    </section>

    <section>
      <div class="section-head">Timeline</div>
      <div class="content">
        <div id="timeline" class="timeline"></div>
      </div>
    </section>
  </main>

  <script>
    const $ = (id) => document.getElementById(id);
    let busy = false;

    async function api(path, options = {}) {
      const response = await fetch(path, {headers: {'Content-Type': 'application/json'}, ...options});
      const data = await response.json();
      if (!response.ok) throw new Error(data.error || response.statusText);
      return data;
    }

    function render(snapshot) {
      $('invoice').textContent = snapshot.invoice_id || 'none';
      $('state').textContent = snapshot.current_state || 'none';
      $('model').textContent = snapshot.model || 'bedrock';

      const facts = snapshot.facts || {};
      const factRows = [
        ['Vendor', facts.vendor_name || facts.vendor_id || 'unknown'],
        ['Invoice #', facts.invoice_number || 'unknown'],
        ['Amount', money(facts.amount_cents, facts.currency)],
        ['Bank', facts.bank_fingerprint || 'unknown'],
        ['Trusted', facts.trusted_vendor ? 'yes' : 'no'],
        ['Due', facts.due_date || 'unknown']
      ];
      $('facts').innerHTML = '';
      factRows.forEach(([label, value]) => {
        const item = document.createElement('div');
        item.className = 'fact';
        item.innerHTML = '<span>' + label + '</span><strong>' + escapeHTML(value) + '</strong>';
        $('facts').appendChild(item);
      });

      if (snapshot.last_proposal) {
        $('proposal-action').textContent = snapshot.last_proposal.action || 'NO_ACTION';
        $('proposal-reason').textContent = snapshot.last_proposal.reasoning_summary || 'No reason returned.';
      } else {
        $('proposal-action').textContent = 'No proposal';
        $('proposal-reason').textContent = 'Waiting for input.';
      }

      $('allowed').innerHTML = '';
      const allowed = snapshot.allowed_actions || [];
      if (allowed.length === 0) {
        const item = document.createElement('span');
        item.className = 'pill';
        item.textContent = 'no actions currently allowed';
        $('allowed').appendChild(item);
      } else {
        allowed.forEach((name) => {
          const item = document.createElement('span');
          item.className = 'pill';
          item.textContent = name;
          $('allowed').appendChild(item);
        });
      }

      $('held').innerHTML = '';
      if (snapshot.held) {
        const wrap = document.createElement('div');
        wrap.className = 'held';
        wrap.innerHTML = '<h2>' + snapshot.held.action_name + ' held</h2><p>' + escapeHTML(snapshot.held.reason) + '</p>';
        const row = document.createElement('div');
        row.className = 'button-row';
        const approve = document.createElement('button');
        approve.className = 'primary';
        approve.textContent = 'Approve';
        approve.onclick = () => review(true);
        const deny = document.createElement('button');
        deny.className = 'danger';
        deny.textContent = 'Deny';
        deny.onclick = () => review(false);
        row.append(approve, deny);
        wrap.appendChild(row);
        $('held').appendChild(wrap);
      }

      $('payments').innerHTML = '';
      const payments = snapshot.payments || [];
      if (payments.length > 0) {
        const wrap = document.createElement('div');
        wrap.className = 'payments';
        const latest = payments[payments.length - 1];
        wrap.innerHTML = '<h2>Payment released</h2><p>' + latest.payment_id + ' · ' + money(latest.amount_cents, latest.currency) + ' · ' + latest.vendor_id + '</p>';
        $('payments').appendChild(wrap);
      }

      $('lines').innerHTML = '';
      (snapshot.lines || []).forEach((line) => {
        const item = document.createElement('div');
        item.className = 'line kind-' + line.kind;
        item.innerHTML = '<small>' + line.kind + '</small>' + escapeHTML(line.text);
        $('lines').appendChild(item);
      });
      if ((snapshot.lines || []).length === 0) $('lines').innerHTML = '<div class="empty">No entries.</div>';
      $('lines').scrollTop = $('lines').scrollHeight;

      $('timeline').innerHTML = '';
      (snapshot.timeline || []).forEach((event) => {
        const item = document.createElement('div');
        item.className = 'event';
        const detail = event.detail ? ' [' + event.detail + ']' : '';
        item.innerHTML = '<small>' + event.kind + ' / ' + (event.actor_id || 'system') + '</small>' + escapeHTML(event.message + detail);
        $('timeline').appendChild(item);
      });
      if ((snapshot.timeline || []).length === 0) $('timeline').innerHTML = '<div class="empty">No events.</div>';
      $('timeline').scrollTop = $('timeline').scrollHeight;
    }

    async function submitInput() {
      if (busy) return;
      busy = true;
      $('submit').disabled = true;
      try {
        render(await api('/api/input', {method: 'POST', body: JSON.stringify({text: $('input').value})}));
      } catch (error) {
        alert(error.message);
      } finally {
        busy = false;
        $('submit').disabled = false;
      }
    }

    async function review(granted) {
      render(await api('/api/approval', {method: 'POST', body: JSON.stringify({granted})}));
    }

    function money(cents, currency) {
      if (!cents) return 'unknown';
      return (currency || 'USD') + ' ' + (Number(cents) / 100).toLocaleString(undefined, {minimumFractionDigits: 2, maximumFractionDigits: 2});
    }

    function escapeHTML(value) {
      return String(value).replace(/[&<>"']/g, (char) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#039;'}[char]));
    }

    $('submit').onclick = submitInput;
    $('reset').onclick = async () => render(await api('/api/reset', {method: 'POST'}));
    document.querySelectorAll('[data-example]').forEach((button) => {
      button.onclick = () => { $('input').value = button.dataset.example; };
    });
    api('/api/state').then(render);
  </script>
</body>
</html>`
