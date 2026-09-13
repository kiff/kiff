package limit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiff/kiff/pkg/kiff/limit"
)

func refundLimit(max int64) limit.Limit {
	return limit.Limit{
		ID:      "refund-agent-daily",
		Subject: "refund-agent",
		Actions: []string{"AUTO_REFUND"},
		Aggregates: []limit.Aggregate{{
			Quantity: limit.Quantity{Parameter: "amount"},
			Max:      max,
			Window:   limit.WindowCalendarDay,
		}},
	}
}

func refundReq(id string, amount int64) limit.Request {
	return limit.Request{
		ID: id, Subject: "refund-agent", Action: "AUTO_REFUND",
		Parameters: map[string]int64{"amount": amount},
	}
}

func authorize(t *testing.T, l []limit.Limit, req limit.Request, led limit.Ledger, now time.Time) limit.Decision {
	t.Helper()
	d, err := limit.Evaluate(context.Background(), l, req, led, now)
	if err != nil {
		t.Fatalf("evaluate %s: %v", req.ID, err)
	}
	if d.Allowed() {
		if err := led.Record(context.Background(), req.ID, d.Draws); err != nil {
			t.Fatalf("record %s: %v", req.ID, err)
		}
	}
	return d
}

// The whole argument in one test: three refunds, each correct in every
// other way, and the third refused while being smaller than the two
// that went through. No per-call check can produce this, because none
// of them can see the other two.
func TestAnActionCorrectInEveryWayIsRefusedOnTheTotal(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	led := limit.NewMemoryLedger()
	limits := []limit.Limit{refundLimit(110000)}

	if d := authorize(t, limits, refundReq("p1", 4200), led, now); !d.Allowed() {
		t.Fatalf("first refund refused: %s", d.Reason)
	}
	if d := authorize(t, limits, refundReq("p2", 99000), led, now); !d.Allowed() {
		t.Fatalf("second refund refused: %s", d.Reason)
	}

	d := authorize(t, limits, refundReq("p3", 8800), led, now)
	if d.Allowed() {
		t.Fatal("the third refund was allowed; 4200+99000+8800 exceeds 110000")
	}
	if d.Outcome != limit.OutcomeLimitReached {
		t.Errorf("outcome = %q, want limit_reached", d.Outcome)
	}
	if d.LimitID != "refund-agent-daily" {
		t.Errorf("LimitID = %q; the refusal must name which limit bound it", d.LimitID)
	}
}

// Revoking a limit must not make the subject less bounded than before.
//
// The obvious implementation filters to the limits in force, and an
// empty set then reads as "nothing covers this action" — which, because
// a limit constrains rather than confers, means allowed. Revocation
// would become the act that removes the bound.
func TestRevokingALimitDoesNotWidenAuthority(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	led := limit.NewMemoryLedger()

	revoked := refundLimit(110000)
	revoked.RevokedAt = now.Add(-time.Hour)

	d := authorize(t, []limit.Limit{revoked}, refundReq("p1", 1), led, now)
	if d.Allowed() {
		t.Fatal("a revoked limit allowed the action; revoking made the subject unbounded")
	}
	if d.Outcome != limit.OutcomeExpired {
		t.Errorf("outcome = %q, want expired", d.Outcome)
	}
}

// A limit constrains; it never confers. A subject with no limit is
// unaffected, so a first limit cannot break a running system.
func TestNoLimitMeansNoOpinion(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	led := limit.NewMemoryLedger()

	d := authorize(t, nil, refundReq("p1", 999999999), led, now)
	if !d.Allowed() {
		t.Fatal("an action with no limit covering it was refused")
	}

	// A limit for a different subject is equally silent.
	other := refundLimit(1)
	other.Subject = "someone-else"
	if d := authorize(t, []limit.Limit{other}, refundReq("p2", 999999), led, now); !d.Allowed() {
		t.Fatal("a limit bound to another subject refused this one")
	}
}

// A retry carrying the same request id must not count twice. Excluding
// it also repairs a draw written just before a crash: the retry
// recomputes from the same base and overwrites its own line.
func TestARetryDoesNotDrawTwice(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	led := limit.NewMemoryLedger()
	limits := []limit.Limit{refundLimit(100)}

	if d := authorize(t, limits, refundReq("same-id", 60), led, now); !d.Allowed() {
		t.Fatal("first attempt refused")
	}
	// The caller never saw the response and retries the same request.
	if d := authorize(t, limits, refundReq("same-id", 60), led, now); !d.Allowed() {
		t.Fatalf("the retry was refused; it counted against itself: %s", d.Reason)
	}

	// A genuinely different request still meets the ceiling.
	if d := authorize(t, limits, refundReq("other-id", 60), led, now); d.Allowed() {
		t.Fatal("a second distinct refund of 60 was allowed against a ceiling of 100")
	}
}

// The tightest limit binds, and adding a limit can only narrow
// authority — never widen it.
func TestTheTightestLimitBinds(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	led := limit.NewMemoryLedger()

	wide := refundLimit(1000000)
	tight := refundLimit(50)
	tight.ID = "refund-agent-tight"

	d := authorize(t, []limit.Limit{wide, tight}, refundReq("p1", 100), led, now)
	if d.Allowed() {
		t.Fatal("the wider limit let an action past the tighter one")
	}
	if d.LimitID != "refund-agent-tight" {
		t.Errorf("LimitID = %q, want the tighter limit", d.LimitID)
	}
}

// An unreadable ledger refuses. Allowing here means acting on authority
// nobody could confirm, which is the thing a limit exists to prevent.
func TestAnUnreadableLedgerRefuses(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()

	_, err := limit.Evaluate(context.Background(), []limit.Limit{refundLimit(100)},
		refundReq("p1", 1), brokenLedger{}, now)
	if err == nil {
		t.Fatal("evaluation succeeded with an unreadable ledger")
	}
	if !errors.Is(err, limit.ErrUsageUnavailable) {
		t.Errorf("err = %v, want ErrUsageUnavailable so callers can refuse deliberately", err)
	}
}

type brokenLedger struct{}

func (brokenLedger) Used(context.Context, limit.Key, time.Time, string) (int64, error) {
	return 0, errors.New("ledger unreachable")
}
func (brokenLedger) Record(context.Context, string, []limit.Draw) error { return nil }

// Counting actions needs no parameter at all, which is how a limit
// bounds something that has no currency — three deploys an hour.
func TestCountingActions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	led := limit.NewMemoryLedger()
	limits := []limit.Limit{{
		ID: "deploys", Subject: "deploy-bot",
		Aggregates: []limit.Aggregate{{Max: 3, Window: limit.WindowRolling1h}},
	}}

	req := func(id string) limit.Request {
		return limit.Request{ID: id, Subject: "deploy-bot", Action: "ROLL_BACK"}
	}
	for _, id := range []string{"d1", "d2", "d3"} {
		if d := authorize(t, limits, req(id), led, now); !d.Allowed() {
			t.Fatalf("%s refused: %s", id, d.Reason)
		}
	}
	if d := authorize(t, limits, req("d4"), led, now); d.Allowed() {
		t.Fatal("a fourth deploy was allowed against a ceiling of three")
	}
	// An empty Actions list covers every action, so a different action
	// by the same subject draws on the same ceiling.
	if d := authorize(t, limits, limit.Request{ID: "d5", Subject: "deploy-bot", Action: "SCALE"},
		led, now); d.Allowed() {
		t.Fatal("a limit with no Actions should cover every action by the subject")
	}
}

// A limit summing a parameter the action did not carry draws nothing
// rather than refusing. Whether the parameter is required belongs to
// the action contract, which has already run by this point.
func TestAMissingSummedParameterDrawsNothing(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	led := limit.NewMemoryLedger()

	d := authorize(t, []limit.Limit{refundLimit(100)},
		limit.Request{ID: "p1", Subject: "refund-agent", Action: "AUTO_REFUND"}, led, now)
	if !d.Allowed() {
		t.Fatalf("refused for a parameter the limit sums but the action omitted: %s", d.Reason)
	}
	if len(d.Draws) != 0 {
		t.Errorf("Draws = %v, want none", d.Draws)
	}
}

func TestWindowStart(t *testing.T) {
	t.Parallel()
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	now := time.Date(2026, 9, 13, 0, 30, 0, 0, time.UTC) // 02:30 in Paris

	got, err := limit.WindowCalendarDay.Start(now, paris)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 13, 0, 0, 0, 0, paris)
	if !got.Equal(want) {
		t.Errorf("calendar day start = %s, want %s (the subject's midnight, not UTC's)", got, want)
	}

	if _, err := limit.Window("fortnight").Start(now, nil); !errors.Is(err, limit.ErrUnknownWindow) {
		t.Errorf("unknown window err = %v, want ErrUnknownWindow", err)
	}
}

func TestValidateRefusesWhatCannotBeEnforced(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*limit.Limit){
		"no id":          func(l *limit.Limit) { l.ID = "" },
		"no subject":     func(l *limit.Limit) { l.Subject = "" },
		"no aggregates":  func(l *limit.Limit) { l.Aggregates = nil },
		"zero ceiling":   func(l *limit.Limit) { l.Aggregates[0].Max = 0 },
		"unknown window": func(l *limit.Limit) { l.Aggregates[0].Window = "fortnight" },
		"never in force": func(l *limit.Limit) {
			l.ValidFrom = time.Now().Add(time.Hour)
			l.ValidUntil = time.Now()
		},
	} {
		l := refundLimit(100)
		l.Aggregates = append([]limit.Aggregate{}, l.Aggregates...)
		mutate(&l)
		if err := l.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := refundLimit(100).Validate(); err != nil {
		t.Errorf("a well-formed limit was refused: %v", err)
	}
}

func TestParseQuantity(t *testing.T) {
	t.Parallel()
	q, err := limit.ParseQuantity("count")
	if err != nil || !q.Counting() {
		t.Errorf(`ParseQuantity("count") = %v, %v`, q, err)
	}
	q, err = limit.ParseQuantity("sum(amount_cents)")
	if err != nil || q.Parameter != "amount_cents" {
		t.Errorf(`ParseQuantity("sum(amount_cents)") = %v, %v`, q, err)
	}
	if q.String() != "sum(amount_cents)" {
		t.Errorf("String() = %q", q.String())
	}
	for _, bad := range []string{"", "sum()", "total(amount)", "sum amount"} {
		if _, err := limit.ParseQuantity(bad); err == nil {
			t.Errorf("ParseQuantity(%q) accepted", bad)
		}
	}
}

// The statement is what a person reads to answer "what has this agent
// been doing with the authority we gave it".
func TestStatementReportsWhatWasDrawn(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	led := limit.NewMemoryLedger()
	l := refundLimit(110000)

	authorize(t, []limit.Limit{l}, refundReq("p1", 4200), led, now)
	authorize(t, []limit.Limit{l}, refundReq("p2", 99000), led, now)

	st, err := limit.StatementFor(context.Background(), l, led, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 1 {
		t.Fatalf("want one statement line, got %d", len(st))
	}
	if st[0].Used != 103200 || st[0].Remaining != 6800 {
		t.Errorf("used=%d remaining=%d, want 103200 and 6800", st[0].Used, st[0].Remaining)
	}
}

// An unreadable ledger must not render as an unused limit.
func TestStatementFailsRatherThanReportingZero(t *testing.T) {
	t.Parallel()
	if _, err := limit.StatementFor(context.Background(), refundLimit(100), brokenLedger{}, time.Now()); err == nil {
		t.Fatal("StatementFor returned usage from an unreadable ledger")
	}
}
