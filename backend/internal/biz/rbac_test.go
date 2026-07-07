package biz

import (
	"testing"

	"github.com/opscenter/ai-gateway/internal/data/model"
)

func TestBootstrapPrincipalHasEveryRoleEverywhere(t *testing.T) {
	p := BootstrapPrincipal()
	for _, tenantID := range []uint{0, 1, 999} {
		if !p.HasRole(tenantID, "owner") {
			t.Fatalf("bootstrap principal should satisfy owner for tenant %d", tenantID)
		}
	}
}

func TestSessionPrincipalTenantRoleScoping(t *testing.T) {
	p := &Principal{
		Kind:        PrincipalKindSession,
		TenantRoles: map[uint]string{1: "admin", 2: "viewer"},
	}
	if !p.HasRole(1, "admin") {
		t.Fatal("expected admin role to satisfy admin requirement")
	}
	if p.HasRole(1, "owner") {
		t.Fatal("admin must not satisfy owner requirement")
	}
	if p.HasRole(2, "member") {
		t.Fatal("viewer must not satisfy member requirement")
	}
	if p.HasRole(3, "viewer") {
		t.Fatal("no membership in tenant 3 must deny access")
	}
}

func TestAdminKeyPrincipalScoping(t *testing.T) {
	tenantScoped := &Principal{Kind: PrincipalKindAdminKey, AdminKeyTenantID: 5, AdminKeyRole: "admin"}
	if !tenantScoped.HasRole(5, "admin") {
		t.Fatal("expected tenant-scoped admin key to satisfy its own tenant")
	}
	if tenantScoped.HasRole(6, "viewer") {
		t.Fatal("tenant-scoped admin key must not reach a different tenant")
	}

	platformWide := &Principal{Kind: PrincipalKindAdminKey, AdminKeyTenantID: 0, AdminKeyRole: "member"}
	if !platformWide.HasRole(999, "member") {
		t.Fatal("platform-wide admin key (tenantID 0) should reach any tenant at its role")
	}
	if platformWide.HasRole(999, "admin") {
		t.Fatal("platform-wide admin key must not exceed its configured role")
	}
}

func TestNilPrincipalDeniesEverything(t *testing.T) {
	var p *Principal
	if p.HasRole(0, "viewer") {
		t.Fatal("nil principal must never satisfy a role check")
	}
}

func TestAllowedTenantIDs(t *testing.T) {
	if ids := BootstrapPrincipal().AllowedTenantIDs(); ids != nil {
		t.Fatalf("platform admin should return nil (unscoped), got %v", ids)
	}
	p := &Principal{Kind: PrincipalKindSession, TenantRoles: map[uint]string{1: "owner", 2: "member"}}
	ids := p.AllowedTenantIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 tenant ids, got %v", ids)
	}
	scoped := &Principal{Kind: PrincipalKindAdminKey, AdminKeyTenantID: 7}
	if ids := scoped.AllowedTenantIDs(); len(ids) != 1 || ids[0] != 7 {
		t.Fatalf("expected [7], got %v", ids)
	}
}

func TestRoleRankOrdering(t *testing.T) {
	roles := []string{"viewer", "member", "admin", "owner"}
	for i := 1; i < len(roles); i++ {
		lo, hi := roles[i-1], roles[i]
		if !(model.RoleRank(lo) < model.RoleRank(hi)) {
			t.Fatalf("expected %s < %s in rank", lo, hi)
		}
	}
	if model.RoleRank("nonsense-role") >= model.RoleRank("viewer") {
		t.Fatal("unknown role must rank below viewer")
	}
}
