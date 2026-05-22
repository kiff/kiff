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
type SimplePolicy struct {
	mu               sync.RWMutex
	actorPermissions map[string]map[Permission]struct{}
	rolePermissions  map[string]map[Permission]struct{}
}

// SimplePermissionPolicy is kept as an explicit alias for readability.
type SimplePermissionPolicy = SimplePolicy

// NewSimplePolicy creates an empty permission policy.
func NewSimplePolicy() *SimplePolicy {
	return &SimplePolicy{
		actorPermissions: map[string]map[Permission]struct{}{},
		rolePermissions:  map[string]map[Permission]struct{}{},
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

// Can returns true when the actor has the permission directly or through a role.
func (p *SimplePolicy) Can(ctx context.Context, a actor.Actor, perm Permission) bool {
	if ctx.Err() != nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	if _, ok := p.actorPermissions[a.ID][perm]; ok {
		return true
	}
	for _, role := range a.Roles {
		if _, ok := p.rolePermissions[role][perm]; ok {
			return true
		}
	}
	return false
}
