package shared

import (
	"testing"

	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
)

func TestBuildIdentityForAgent(t *testing.T) {
	cfg := config.Default()
	cfg.Mode = model.ModeAgent
	cfg.Source.Kind = model.SourceEtcd
	cfg.Source.Etcd.Namespace = "/microservice/lhdht"

	identity := buildIdentity(&cfg, Params{
		Mode:        model.ModeAgent,
		ServiceName: "service-mesh-agent",
	})

	if identity.Service != "service-mesh-agent" {
		t.Fatalf("unexpected service: %s", identity.Service)
	}
	if identity.Namespace != "/microservice/lhdht" {
		t.Fatalf("unexpected namespace: %s", identity.Namespace)
	}
	if identity.NodeID == "" {
		t.Fatal("expected node id")
	}
	if identity.DataplaneID == "" {
		t.Fatal("expected dataplane id")
	}
	if identity.DataplaneID == identity.NodeID {
		t.Fatal("expected agent dataplane id to be more specific than node id")
	}
}

func TestBuildIdentityForSidecar(t *testing.T) {
	cfg := config.Default()

	identity := buildIdentity(&cfg, Params{
		Mode:        model.ModeSidecar,
		ServiceName: "config",
		InstanceID:  "config-1",
		Namespace:   "/microservice/lhdht",
		Env:         "dev",
	})

	if identity.DataplaneID != "config-1" {
		t.Fatalf("unexpected dataplane id: %s", identity.DataplaneID)
	}
	if identity.NodeID != "config-1" {
		t.Fatalf("unexpected node id: %s", identity.NodeID)
	}
	if identity.Service != "config" {
		t.Fatalf("unexpected service: %s", identity.Service)
	}
	if identity.Namespace != "/microservice/lhdht" {
		t.Fatalf("unexpected namespace: %s", identity.Namespace)
	}
	if identity.Env != "dev" {
		t.Fatalf("unexpected env: %s", identity.Env)
	}
}
