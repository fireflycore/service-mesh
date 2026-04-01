package originalidentity

import (
	"testing"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
)

func TestExtract(t *testing.T) {
	identity := Extract([]*invokev1.MetadataEntry{
		{Key: MetadataUserID, Values: []string{"user-1"}},
		{Key: MetadataSubject, Values: []string{"alice@example.com"}},
		{Key: MetadataIssuer, Values: []string{"gateway"}},
	})

	if got, want := identity.UserID, "user-1"; got != want {
		t.Fatalf("unexpected user id: got=%s want=%s", got, want)
	}
	if got, want := identity.Subject, "alice@example.com"; got != want {
		t.Fatalf("unexpected subject: got=%s want=%s", got, want)
	}
	if got, want := identity.Issuer, "gateway"; got != want {
		t.Fatalf("unexpected issuer: got=%s want=%s", got, want)
	}
	if !identity.Present() {
		t.Fatal("expected identity to be present")
	}
}

func TestResolve(t *testing.T) {
	effective := Resolve(&invokev1.InvocationContext{
		Caller: &invokev1.Caller{
			Service:   "gateway",
			Namespace: "default",
			Env:       "dev",
		},
		Metadata: []*invokev1.MetadataEntry{
			{Key: MetadataSubject, Values: []string{"alice@example.com"}},
		},
	})

	if got, want := effective.Source, SourceMetadata; got != want {
		t.Fatalf("unexpected source: got=%s want=%s", got, want)
	}
	if got, want := effective.Trust, TrustUnverified; got != want {
		t.Fatalf("unexpected trust: got=%s want=%s", got, want)
	}
	if got, want := effective.Caller.Service, "gateway"; got != want {
		t.Fatalf("unexpected caller service: got=%s want=%s", got, want)
	}
	principal := effective.Principal()
	if got, want := principal.Kind, PrincipalOriginalUser; got != want {
		t.Fatalf("unexpected principal kind: got=%s want=%s", got, want)
	}
	if got, want := principal.Subject, "alice@example.com"; got != want {
		t.Fatalf("unexpected principal subject: got=%s want=%s", got, want)
	}
	extensions := effective.ContextExtensions()
	if got, want := extensions["caller_service"], "gateway"; got != want {
		t.Fatalf("unexpected caller_service extension: got=%s want=%s", got, want)
	}
	if got, want := extensions["original_user_trust"], TrustUnverified; got != want {
		t.Fatalf("unexpected original_user_trust extension: got=%s want=%s", got, want)
	}
	if got, want := extensions["effective_principal_kind"], PrincipalOriginalUser; got != want {
		t.Fatalf("unexpected effective_principal_kind extension: got=%s want=%s", got, want)
	}
}

func TestResolveFallsBackToCallerPrincipal(t *testing.T) {
	effective := Resolve(&invokev1.InvocationContext{
		Caller: &invokev1.Caller{
			Service: "orders",
		},
	})

	principal := effective.Principal()
	if got, want := principal.Kind, PrincipalCaller; got != want {
		t.Fatalf("unexpected principal kind: got=%s want=%s", got, want)
	}
	if got, want := principal.Subject, "orders"; got != want {
		t.Fatalf("unexpected principal subject: got=%s want=%s", got, want)
	}
	if got, want := principal.Trust, TrustLocal; got != want {
		t.Fatalf("unexpected principal trust: got=%s want=%s", got, want)
	}
}
