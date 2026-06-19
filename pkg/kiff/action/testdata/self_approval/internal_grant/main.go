package main

import (
	"github.com/kiff/kiff/pkg/kiff/action"
	"github.com/kiff/kiff/pkg/kiff/internal/trust"
)

func main() {
	ctx := action.ActionContext{}
	ctx.GrantApproval(trust.Grant{})
}
