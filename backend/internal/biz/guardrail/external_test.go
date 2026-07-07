package guardrail

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"

	guardrailv1 "github.com/opscenter/ai-gateway/api/guardrail/v1"
)

// fakeGuardrailServer is a minimal, real gRPC server implementing the
// GuardrailEngine contract, so ExternalChecker is tested against the actual
// wire protocol rather than a mocked client.
type fakeGuardrailServer struct {
	guardrailv1.UnimplementedGuardrailEngineServer
	response *guardrailv1.CheckResponse
	lastReq  *guardrailv1.CheckRequest
}

func (s *fakeGuardrailServer) Check(_ context.Context, req *guardrailv1.CheckRequest) (*guardrailv1.CheckResponse, error) {
	s.lastReq = req
	return s.response, nil
}

func startFakeGuardrailServer(t *testing.T, resp *guardrailv1.CheckResponse) (addr string, srv *fakeGuardrailServer, stop func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	fake := &fakeGuardrailServer{response: resp}
	guardrailv1.RegisterGuardrailEngineServer(grpcSrv, fake)
	go grpcSrv.Serve(lis)
	return lis.Addr().String(), fake, grpcSrv.Stop
}

func TestExternalCheckerRoundTrip(t *testing.T) {
	addr, fake, stop := startFakeGuardrailServer(t, &guardrailv1.CheckResponse{
		Action: "redact", Types: []string{"person_name"}, RedactedText: "Hello ***",
	})
	defer stop()

	checker, err := NewExternalChecker("external", addr, 2*time.Second, "acme")
	if err != nil {
		t.Fatalf("new checker: %v", err)
	}
	finding, err := checker.Check(context.Background(), &Content{Text: "Hello Alice"}, DirectionOutbound)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if finding.Action != ActionRedact || finding.Redacted != "Hello ***" || len(finding.Types) != 1 || finding.Types[0] != "person_name" {
		t.Fatalf("unexpected finding: %+v", finding)
	}
	if fake.lastReq.Text != "Hello Alice" || fake.lastReq.Direction != "outbound" || fake.lastReq.TenantName != "acme" {
		t.Fatalf("unexpected request seen by server: %+v", fake.lastReq)
	}
}

func TestExternalCheckerNoneAction(t *testing.T) {
	addr, _, stop := startFakeGuardrailServer(t, &guardrailv1.CheckResponse{Action: "none"})
	defer stop()

	checker, err := NewExternalChecker("external", addr, 2*time.Second, "")
	if err != nil {
		t.Fatalf("new checker: %v", err)
	}
	finding, err := checker.Check(context.Background(), &Content{Text: "clean text"}, DirectionInbound)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if finding.Action != ActionNone {
		t.Fatalf("expected none, got %+v", finding)
	}
}

func TestExternalCheckerUnreachableServerErrors(t *testing.T) {
	// Nothing listening on this address — Check must return an error (which
	// the chain's fail-open/closed policy then governs), not panic or hang.
	checker, err := NewExternalChecker("external", "127.0.0.1:1", 300*time.Millisecond, "")
	if err != nil {
		t.Fatalf("new checker (dial is lazy, should not fail here): %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := checker.Check(ctx, &Content{Text: "x"}, DirectionInbound); err == nil {
		t.Fatal("expected an error calling an unreachable external engine")
	}
}
