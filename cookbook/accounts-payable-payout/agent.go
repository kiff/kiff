package payables

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type InvoiceFacts struct {
	InvoiceID       string `json:"invoice_id"`
	VendorID        string `json:"vendor_id"`
	VendorName      string `json:"vendor_name"`
	InvoiceNumber   string `json:"invoice_number"`
	AmountCents     int64  `json:"amount_cents"`
	Currency        string `json:"currency"`
	BankFingerprint string `json:"bank_fingerprint"`
	DueDate         string `json:"due_date"`
	TrustedVendor   bool   `json:"trusted_vendor"`
}

type AgentRequest struct {
	InvoiceID      string       `json:"invoice_id"`
	CurrentState   string       `json:"current_state"`
	AllowedActions []string     `json:"allowed_actions"`
	InvoiceFacts   InvoiceFacts `json:"invoice_facts"`
	OperatorInput  string       `json:"operator_input"`
}

type AgentProposal struct {
	ActionName       string         `json:"action"`
	Parameters       map[string]any `json:"parameters,omitempty"`
	ReasoningSummary string         `json:"reasoning_summary,omitempty"`
	Confidence       float64        `json:"confidence,omitempty"`
	Raw              string         `json:"raw,omitempty"`
}

type Agent interface {
	Propose(context.Context, AgentRequest) (AgentProposal, error)
}

type BedrockAgent struct {
	Region  string
	ModelID string
}

func NewBedrockAgent(region, modelID string) BedrockAgent {
	if region == "" {
		region = "us-east-1"
	}
	if modelID == "" {
		modelID = "us.anthropic.claude-haiku-4-5-20251001-v1:0"
	}
	return BedrockAgent{Region: region, ModelID: modelID}
}

func (a BedrockAgent) Propose(ctx context.Context, request AgentRequest) (AgentProposal, error) {
	prompt, err := agentPrompt(request)
	if err != nil {
		return AgentProposal{}, err
	}

	messages, err := json.Marshal([]map[string]any{
		{
			"role": "user",
			"content": []map[string]string{
				{"text": prompt},
			},
		},
	})
	if err != nil {
		return AgentProposal{}, err
	}

	inferenceConfig, err := json.Marshal(map[string]any{
		"maxTokens":   700,
		"temperature": 0.1,
	})
	if err != nil {
		return AgentProposal{}, err
	}

	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	cmd := exec.CommandContext(callCtx,
		"aws", "bedrock-runtime", "converse",
		"--region", a.Region,
		"--model-id", a.ModelID,
		"--messages", string(messages),
		"--inference-config", string(inferenceConfig),
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	output, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return AgentProposal{}, fmt.Errorf("bedrock converse failed: %s", message)
	}

	text, err := bedrockText(output)
	if err != nil {
		return AgentProposal{}, err
	}
	proposal, err := parseProposal(text)
	if err != nil {
		return AgentProposal{}, err
	}
	proposal.Raw = text
	return proposal, nil
}

func agentPrompt(request AgentRequest) (string, error) {
	body, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`You are a real accounts-payable agent operating behind KIFF.

You do not release money directly. You propose exactly one domain action. KIFF validates state, permissions, parameters, approvals, and executor ownership before any side effect.

Domain states:
- RECEIVED: invoice exists but is not verified.
- NEEDS_INFO: invoice is waiting for missing details.
- VERIFIED: invoice identity, amount, and rails were checked.
- READY_FOR_PAYMENT: payment can be decided.
- PAYMENT_HELD: release is waiting for finance approval.
- PAID and REJECTED: terminal states.

Action contracts:
- REQUEST_MISSING_INFO: allowed in RECEIVED. Parameters: missing_fields.
- RECORD_INFO_RECEIVED: allowed in NEEDS_INFO. Parameters: invoice_id, vendor_id, invoice_number, amount_cents, currency, bank_fingerprint.
- VERIFY_INVOICE: allowed in RECEIVED. Parameters: invoice_id, vendor_id, invoice_number, amount_cents, currency, bank_fingerprint.
- MARK_READY_FOR_PAYMENT: allowed in VERIFIED. Parameters: due_date.
- RELEASE_LOW_RISK_PAYMENT: allowed in READY_FOR_PAYMENT only when trusted vendor and amount <= 50000 cents. Parameters: invoice_id, vendor_id, amount_cents, currency, bank_fingerprint, trusted_vendor, idempotency_key.
- HOLD_FOR_APPROVAL: allowed in READY_FOR_PAYMENT for high-risk payment release. Parameters: reason.
- RELEASE_APPROVED_PAYMENT: allowed in PAYMENT_HELD. Human approval required. Payment service executes it, not the agent.
- REJECT_INVOICE: allowed before terminal states. Parameters: reason.
- NO_ACTION: use when no listed action is appropriate.

Rules:
- Return JSON only. No markdown.
- Pick one action from allowed_actions when possible.
- If amount is above 50000 cents, vendor is not trusted, or bank details look changed, prefer HOLD_FOR_APPROVAL instead of RELEASE_LOW_RISK_PAYMENT.
- If the current state is terminal or allowed_actions is empty, return NO_ACTION.
- Never say money has moved. You only propose.
- Fill parameters from invoice_facts and operator_input. Infer conservative values only when harmless.

Current request:
%s

Required JSON shape:
{"action":"REQUEST_MISSING_INFO|RECORD_INFO_RECEIVED|VERIFY_INVOICE|MARK_READY_FOR_PAYMENT|RELEASE_LOW_RISK_PAYMENT|HOLD_FOR_APPROVAL|RELEASE_APPROVED_PAYMENT|REJECT_INVOICE|NO_ACTION","parameters":{},"reasoning_summary":"short operational reason","confidence":0.0}`, string(body)), nil
}

func bedrockText(output []byte) (string, error) {
	var response struct {
		Output struct {
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		} `json:"output"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return "", fmt.Errorf("decode bedrock response: %w", err)
	}
	var parts []string
	for _, content := range response.Output.Message.Content {
		if strings.TrimSpace(content.Text) != "" {
			parts = append(parts, content.Text)
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" {
		return "", errors.New("bedrock response contained no text")
	}
	return text, nil
}

func parseProposal(text string) (AgentProposal, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 || end < start {
		return AgentProposal{}, fmt.Errorf("agent did not return JSON: %q", text)
	}

	var proposal AgentProposal
	if err := json.Unmarshal([]byte(text[start:end+1]), &proposal); err != nil {
		return AgentProposal{}, fmt.Errorf("decode agent proposal: %w: %s", err, text)
	}
	proposal.ActionName = strings.TrimSpace(strings.ToUpper(proposal.ActionName))
	if proposal.ActionName == "" {
		proposal.ActionName = "NO_ACTION"
	}
	if proposal.Parameters == nil {
		proposal.Parameters = map[string]any{}
	}
	return proposal, nil
}
