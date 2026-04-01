package extauthz

import (
	"testing"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/pkg/originalidentity"
)

func TestBuildCheckRequestIncludesOriginalIdentityContext(t *testing.T) {
	req := &invokev1.UnaryInvokeRequest{
		Target: &invokev1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
			Port:      19090,
		},
		Method: "/acme.orders.v1.OrderService/GetOrder",
		Codec:  "proto",
		Context: &invokev1.InvocationContext{
			TraceId: "trace-1",
			Caller: &invokev1.Caller{
				Service: "gateway",
			},
			Metadata: []*invokev1.MetadataEntry{
				{Key: originalidentity.MetadataUserID, Values: []string{"user-1"}},
				{Key: originalidentity.MetadataSubject, Values: []string{"alice@example.com"}},
				{Key: originalidentity.MetadataIssuer, Values: []string{"gateway"}},
			},
		},
	}

	checkReq := buildCheckRequest(req, nil)
	extensions := checkReq.GetAttributes().GetContextExtensions()

	if got, want := extensions["original_user_id"], "user-1"; got != want {
		t.Fatalf("unexpected original user id: got=%s want=%s", got, want)
	}
	if got, want := extensions["original_user_subject"], "alice@example.com"; got != want {
		t.Fatalf("unexpected original user subject: got=%s want=%s", got, want)
	}
	if got, want := extensions["original_user_issuer"], "gateway"; got != want {
		t.Fatalf("unexpected original user issuer: got=%s want=%s", got, want)
	}
	if got, want := extensions["original_user_source"], originalidentity.SourceMetadata; got != want {
		t.Fatalf("unexpected original user source: got=%s want=%s", got, want)
	}
	if got, want := extensions["original_user_trust"], originalidentity.TrustUnverified; got != want {
		t.Fatalf("unexpected original user trust: got=%s want=%s", got, want)
	}
	if got, want := extensions["caller_service"], "gateway"; got != want {
		t.Fatalf("unexpected caller service: got=%s want=%s", got, want)
	}
	if got, want := extensions["effective_principal_kind"], originalidentity.PrincipalOriginalUser; got != want {
		t.Fatalf("unexpected effective principal kind: got=%s want=%s", got, want)
	}
	if got, want := extensions["effective_principal_subject"], "alice@example.com"; got != want {
		t.Fatalf("unexpected effective principal subject: got=%s want=%s", got, want)
	}
}
