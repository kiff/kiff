package actor

// Type describes the kind of participant acting inside a KIFF system.
type Type string

const (
	TypeHuman    Type = "human"
	TypeAgent    Type = "agent"
	TypeService  Type = "service"
	TypeSystem   Type = "system"
	TypeExternal Type = "external"
)

// Actor identifies a human, agent, service, system, or external integration.
type Actor struct {
	ID          string
	Type        Type
	DisplayName string
	Roles       []string
}
