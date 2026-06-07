// Package trust holds an unforgeable in-module capability that gates
// privileged state changes on an action context.
//
// It exists to close the self-approval trust boundary. The approved bit
// on action.ActionContext must be settable only from inside the
// framework's trust boundary — the runtime, after it has consulted the
// approval store — and never by a caller that merely imports the action
// package.
//
// Because this is an internal package, only code rooted at
// github.com/kiffhq/kiff can import it; an external embedder cannot. And
// because Grant carries an unexported field, no external package can
// construct an equivalent value via an anonymous struct (struct identity
// requires unexported field names to originate in the same package). The
// two together mean a Grant can only be minted from within the framework.
package trust

// Grant is a capability proving the holder is inside the framework's
// trust boundary. The unexported field makes it unconstructable — and
// even un-nameable — from any package that cannot import this one.
type Grant struct {
	ok bool
}

// NewGrant mints a Grant. It is callable only from within the module,
// since the package is internal.
func NewGrant() Grant {
	return Grant{ok: true}
}
