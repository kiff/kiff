package main

import (
	"context"
	"fmt"
	"os"

	payables "github.com/kiff/kiff/cookbook/accounts-payable-payout"
)

type scriptedAgent struct {
	proposals []payables.AgentProposal
}

func (a *scriptedAgent) Propose(context.Context, payables.AgentRequest) (payables.AgentProposal, error) {
	if len(a.proposals) == 0 {
		return payables.AgentProposal{
			ActionName:       "NO_ACTION",
			ReasoningSummary: "scripted demo has no remaining proposals",
			Confidence:       1,
		}, nil
	}
	next := a.proposals[0]
	a.proposals = a.proposals[1:]
	return next, nil
}

func main() {
	ctx := context.Background()
	agent := &scriptedAgent{proposals: []payables.AgentProposal{
		{
			ActionName: payables.ActionVerifyInvoice,
			Parameters: map[string]any{
				"invoice_id":       "inv-ap-7741",
				"vendor_id":        "vendor-northwind",
				"invoice_number":   "INV-7741",
				"amount_cents":     1842000,
				"currency":         "USD",
				"bank_fingerprint": "bank-ach-9912",
			},
			ReasoningSummary: "invoice facts are complete and payment rails are known",
			Confidence:       0.95,
		},
		{
			ActionName:       payables.ActionMarkReadyForPayment,
			Parameters:       map[string]any{"due_date": "2026-07-15"},
			ReasoningSummary: "verified invoice is ready for payment decision",
			Confidence:       0.93,
		},
		{
			ActionName:       payables.ActionHoldForApproval,
			Parameters:       map[string]any{"reason": "amount is above autonomous release threshold"},
			ReasoningSummary: "high-value payment requires finance approval",
			Confidence:       0.98,
		},
		{
			ActionName:       "NO_ACTION",
			ReasoningSummary: "invoice is already paid; duplicate payment request is refused",
			Confidence:       1,
		},
	}}

	app, err := payables.NewInteractiveApp(agent, "scripted-agent")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create app: %v\n", err)
		os.Exit(1)
	}

	steps := []string{
		"Invoice INV-7741 from Northwind Parts for $18,420.00 USD, vendor vendor-northwind, bank ACH-9912, due 2026-07-15.",
		"Mark the verified invoice ready for payment.",
		"Pay this invoice today.",
	}
	for _, step := range steps {
		if _, err := app.ProcessInput(ctx, step); err != nil {
			fmt.Fprintf(os.Stderr, "process input: %v\n", err)
			os.Exit(1)
		}
	}

	if _, err := app.ReviewHeld(ctx, true); err != nil {
		fmt.Fprintf(os.Stderr, "approve payment: %v\n", err)
		os.Exit(1)
	}

	result, err := app.ProcessInput(ctx, "Pay this invoice again.")
	if err != nil {
		fmt.Fprintf(os.Stderr, "duplicate input: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("KIFF accounts-payable payout demo")
	fmt.Println()
	for _, line := range result.Lines {
		fmt.Printf(" - %-5s %s\n", line.Kind, line.Text)
	}
	fmt.Println()
	fmt.Printf("Final state: %s\n", result.CurrentState)
	if len(result.Payments) > 0 {
		payment := result.Payments[len(result.Payments)-1]
		fmt.Printf("Payment: %s %s %.2f via %s\n", payment.PaymentID, payment.Currency, float64(payment.AmountCents)/100, payment.IdempotencyKey)
	}
}
