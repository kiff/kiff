package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/kiff/kiff/pkg/kiff/actor"
)

// ErrUnauthenticated is returned by an Authenticator that could not establish
// a principal for the request. The handler renders it as 401.
var ErrUnauthenticated = errors.New("unauthenticated")

// Principal is the identity the transport established for a request.
//
// It is the authority counterpart to the self-approval boundary. The actor in
// a request body is a claim; a Principal is a fact the server established. The
// handler overwrites the body's actor with this before anything reads it, so a
// caller cannot act as, or approve as, someone else by editing JSON.
type Principal struct {
	// ActorID identifies the caller. It becomes ActionContext.Actor.ID, which
	// is what the permission policy resolves authority from.
	ActorID string
	// Roles is descriptive metadata carried into audit records. It does not
	// grant authority: permissions resolve through the policy by ActorID, so a
	// role here cannot escalate anything (#19).
	Roles []string
}

// Authenticator establishes the principal for a request.
//
// Implementations must not consult the request body: the body is what the
// caller controls, and the point of this interface is to establish identity
// from something they do not.
type Authenticator interface {
	Authenticate(*http.Request) (Principal, error)
}

// AuthenticatorFunc adapts a function to Authenticator.
type AuthenticatorFunc func(*http.Request) (Principal, error)

// Authenticate calls f.
func (f AuthenticatorFunc) Authenticate(r *http.Request) (Principal, error) {
	return f(r)
}

// StaticTokenAuthenticator maps bearer tokens to principals.
//
// It exists so a self-hosted deployment has a working answer on day one rather
// than an interface to implement, and so the example servers can demonstrate
// the authenticated path. Tokens are compared in constant time. For anything
// beyond a single trusted service, implement Authenticator against your own
// identity provider.
type StaticTokenAuthenticator struct {
	tokens map[string]Principal
}

// NewStaticTokenAuthenticator builds an authenticator from token -> principal.
func NewStaticTokenAuthenticator(tokens map[string]Principal) *StaticTokenAuthenticator {
	copied := make(map[string]Principal, len(tokens))
	for token, principal := range tokens {
		copied[token] = principal
	}
	return &StaticTokenAuthenticator{tokens: copied}
}

// Authenticate reads a bearer token from the Authorization header.
func (a *StaticTokenAuthenticator) Authenticate(r *http.Request) (Principal, error) {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return Principal{}, ErrUnauthenticated
	}
	presented := strings.TrimSpace(header[len(prefix):])
	// Compare against every token so the work does not depend on which token
	// was presented, and never return early on a match.
	var found Principal
	ok := false
	for token, principal := range a.tokens {
		if subtle.ConstantTimeCompare([]byte(token), []byte(presented)) == 1 {
			found, ok = principal, true
		}
	}
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	return found, nil
}

type principalKey struct{}

// withPrincipal stores the authenticated principal on the request context.
func withPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal the handler authenticated for this
// request, if any. Handlers use it to overwrite caller-supplied actors.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// resolveActor returns the actor a request should be attributed to.
//
// When the request was authenticated, the principal wins outright and the
// caller-supplied actor is discarded — including its type and display name,
// because audit attribution must not contain an identity-shaped value the
// caller chose. When it was not (AllowUnauthenticated), the body's actor
// stands, which is exactly why that mode is opt-in.
func resolveActor(ctx context.Context, claimed actor.Actor) actor.Actor {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return claimed
	}
	return actor.Actor{
		ID:    p.ActorID,
		Roles: append([]string(nil), p.Roles...),
		Type:  claimed.Type,
	}
}
