package guardrail

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	guardrailv1 "github.com/opscenter/ai-gateway/api/guardrail/v1"
)

// ExternalChecker calls an operator-run detection service over gRPC (docs/
// design/06-security-and-guardrails.md P2 "external" checker) — Microsoft
// Presidio, a cloud DLP API, or an in-house engine, all behind the one
// GuardrailEngine.Check RPC (api/guardrail/v1/guardrail.proto). This repo is
// the client only; it does not ship a server implementation.
type ExternalChecker struct {
	name    string
	client  guardrailv1.GuardrailEngineClient
	timeout time.Duration
	tenant  string
}

// NewExternalChecker dials target (a plaintext gRPC address — put TLS
// termination in front for production, matching the project's "operator
// supplies the trust boundary" stance elsewhere). Dialing is lazy/non-
// blocking; connection errors surface as Check() errors, which the chain's
// fail-open/closed policy then governs — a down external engine must not by
// itself take down the proxy.
func NewExternalChecker(name, target string, timeout time.Duration, tenant string) (*ExternalChecker, error) {
	if timeout <= 0 {
		timeout = 200 * time.Millisecond
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("guardrail: dial external engine %s: %w", target, err)
	}
	return &ExternalChecker{name: name, client: guardrailv1.NewGuardrailEngineClient(conn), timeout: timeout, tenant: tenant}, nil
}

func (c *ExternalChecker) Name() string { return c.name }
func (c *ExternalChecker) Mode() Mode   { return ModeSync }

func (c *ExternalChecker) Check(ctx context.Context, content *Content, dir Direction) (Finding, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.Check(ctx, &guardrailv1.CheckRequest{
		Text: content.Text, Direction: string(dir), TenantName: c.tenant, CheckerName: c.name,
	})
	if err != nil {
		return Finding{}, fmt.Errorf("guardrail: external engine %s: %w", c.name, err)
	}
	return Finding{
		Action:   Action(resp.Action),
		Types:    resp.Types,
		Details:  resp.Details,
		Redacted: resp.RedactedText,
	}, nil
}
