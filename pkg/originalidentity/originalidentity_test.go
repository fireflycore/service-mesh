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
