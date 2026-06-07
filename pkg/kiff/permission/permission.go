package permission

import (
	"context"
	"sync"

	"github.com/kiffhq/kiff/pkg/kiff/actor"
)

// Permission names an authority required by an action contract.
type Permission string

// Policy answers whether an actor has a permission.
type Policy interface {
	Can(context.Context, actor.Actor, Permission) bool
}

// PermissionPolicy is kept as an explicit alias for readability.
type PermissionPolicy = Policy

// SimplePolicy is an in-memory policy for tests, examples, and small apps.
//
// Authority is resolved from policy-owned state only. Role membership is
// recorded with AssignRole keyed by actor ID; Can resolves an actor's
// roles from that membership, not from actor.Roles on the caller-built
// context. This closes the authority trust boundary (#19): a caller
// cannot self-grant a permission by putting a role on the actor it
// submits. actor.Roles is descriptive metadata (audit/display) and has
// no authorization power.
type SimplePolicy struct {
	mu               sync.RWMutex
	actorPermissions map[string]map[Permission]struct{}
	rolePermissions  map[string]map[Permission]struct{}
	actorRoles       map[string]map[string]struct{}
}

// SimplePermissionPolicy is kept as an explicit alias for readability.
type SimplePermissionPolicy = SimplePolicy

// NewSimplePolicy creates an empty permission policy.
func NewSimplePolicy() *SimplePolicy {
	return &SimplePolicy{
		actorPermissions: map[string]map[Permission]struct{}{},
		rolePermissions:  map[string]map[Permission]struct{}{},
		actorRoles:       map[string]map[string]struct{}{},
	}
}

// NewSimplePermissionPolicy creates an empty permission policy.
func NewSimplePermissionPolicy() *SimplePolicy {
	return NewSimplePolicy()
}

// GrantActor grants a permission directly to an actor id.
func (p *SimplePolicy) GrantActor(actorID string, perm Permission) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.actorPermissions[actorID] == nil {
		p.actorPermissions[actorID] = map[Permission]struct{}{}
	}
	p.actorPermissions[actorID][perm] = struct{}{}
}

// GrantRole grants a permission to all actors with the role.
func (p *SimplePolicy) GrantRole(role string, perm Permission) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rolePermissions[role] == nil {
		p.rolePermissions[role] = map[Permission]struct{}{}
	}
	p.rolePermissions[role][perm] = struct{}{}
}

// AssignRole records that an actor holds a role. Membership is
// policy-owned and keyed by actor ID — this is the trusted source Can
// consults, never the roles on a caller-supplied actor. The host
// assigns roles from an authenticated identity (see #19).
func (p *SimplePolicy) AssignRole(actorID, role string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.actorRoles[actorID] == nil {
		p.actorRoles[actorID] = map[string]struct{}{}
	}
	p.actorRoles[actorID][role] = struct{}{}
}

// RevokeRole removes a role assignment from an actor. A no-op if the
// actor did not hold the role.
func (p *SimplePolicy) RevokeRole(actorID, role string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if roles := p.actorRoles[actorID]; roles != nil {
		delete(roles, role)
		if len(roles) == 0 {
			delete(p.actorRoles, actorID)
		}
	}
}

// Can returns true when the actor has the permission directly or through
// a role the policy has assigned to the actor's ID. Role membership is
// read from policy-owned assignments (AssignRole), never from
// actor.Roles on the caller-built context — that field carries no
// authority (#19).
func (p *SimplePolicy) Can(ctx context.Context, a actor.Actor, perm Permission) bool {
	if ctx.Err() != nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	if _, ok := p.actorPermissions[a.ID][perm]; ok {
		return true
	}
	for role := range p.actorRoles[a.ID] {
		if _, ok := p.rolePermissions[role][perm]; ok {
			return true
		}
	}
	return false
}
