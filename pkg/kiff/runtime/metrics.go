package runtime

// MetricsRecorder receives counter increments from the runtime on the
// successful operational path. Implementations are supplied by the
// adopter through Config.Metrics; the runtime's behavior is unchanged
// when no recorder is configured.
//
// The interface is deliberately narrow. Counters only — no histograms,
// no gauges, no labels beyond a small set of structured attributes —
// because the runtime cannot anticipate every adopter's metering shape.
// Adopters who want richer telemetry compose their own recorder around
// this interface, or wire pkg/kiff/observability for the broader
// audit-derived signal.
//
// Implementations must be safe for concurrent use. The runtime calls
// Inc from goroutines on the request path; blocking the call blocks
// the runtime.
type MetricsRecorder interface {
	// Inc adds n to the counter identified by name and the given
	// attributes. n is unsigned because counters are monotonic; pass
	// 1 for the typical "this happened once" case.
	//
	// name is the canonical counter identifier — see the kiff.* names
	// the runtime emits (kiff.events.ingested, kiff.actions.executed,
	// and so on). Adopters who want a different naming scheme map it
	// inside their recorder implementation.
	Inc(name string, n uint64, attrs ...Attr)
}

// Attr is a structured attribute attached to a counter increment.
// Unlike a generic label set, Attr carries a single key-value pair
// per entry to keep the call-site shape uniform. Adopters who need
// a map-shaped label set assemble one from a slice of Attr values.
type Attr struct {
	Key   string
	Value string
}

// EntityType returns an Attr keyed "entity_type". The runtime emits
// this attribute on every counter increment so that adopters can
// segment counts by the entity the operation acted on (the order in
// a refund domain, the ticket in a support domain, and so on).
func EntityType(value string) Attr { return Attr{Key: "entity_type", Value: value} }

// noopMetrics is the default MetricsRecorder used when Config.Metrics
// is nil. Every call is discarded, with no allocation, so existing
// runtime tests continue to pass without wiring metrics.
type noopMetrics struct{}

// Inc satisfies MetricsRecorder by doing nothing.
func (noopMetrics) Inc(string, uint64, ...Attr) {}

// NoopMetrics is the default MetricsRecorder. It is safe to use as a
// permanent recorder when an adopter does not want metering at all.
var NoopMetrics MetricsRecorder = noopMetrics{}

// Counter names emitted by the runtime. These are the canonical names
// adopters can rely on; adding a new counter is a documented public
// surface change. Removing or renaming one is a breaking change.
const (
	CounterEventsIngested     = "kiff.events.ingested"
	CounterDecisionsRecorded  = "kiff.decisions.recorded"
	CounterActionsValidated   = "kiff.actions.validated"
	CounterActionsExecuted    = "kiff.actions.executed"
	CounterApprovalsRequested = "kiff.approvals.requested"
	CounterApprovalsReviewed  = "kiff.approvals.reviewed"
)
