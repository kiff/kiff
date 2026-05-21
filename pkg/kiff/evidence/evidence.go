package evidence

import "time"

// Kind describes the source category for evidence used by a decision or action.
type Kind string

// EvidenceKind is kept as an explicit alias for readability.
type EvidenceKind = Kind

const (
	KindDocument      Kind = "document"
	KindEvent         Kind = "event"
	KindSystemData    Kind = "system_data"
	KindExternalAPI   Kind = "external_api"
	KindAgentAnalysis Kind = "agent_analysis"
	KindHumanReview   Kind = "human_review"
	KindLog           Kind = "log"
)

// Ref points to supporting material without forcing KIFF to own that material.
type Ref struct {
	ID        string    `json:"id"`
	Kind      Kind      `json:"kind"`
	Source    string    `json:"source,omitempty"`
	URI       string    `json:"uri,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// EvidenceRef is kept as an explicit alias for readability in domain code.
type EvidenceRef = Ref
