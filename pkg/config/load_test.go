package config

import "testing"

func TestLoadReadsFlatAgentAddress(t *testing.T) {
	cfg, err := Load(LoadOptions{
		Path: "..\\..\\testdata\\agent.yaml",
	})
	if err != nil {
		t.Fatalf("load agent config failed: %v", err)
	}

	if got, want := cfg.Runtime.Agent.Address, "127.0.0.1:19090"; got != want {
		t.Fatalf("unexpected agent address: got=%s want=%s", got, want)
	}
}

func TestLoadReadsFlatSidecarAddress(t *testing.T) {
	cfg, err := Load(LoadOptions{
		Path: "..\\..\\testdata\\sidecar.yaml",
	})
	if err != nil {
		t.Fatalf("load sidecar config failed: %v", err)
	}

	if got, want := cfg.Runtime.Sidecar.Address, "127.0.0.1:19091"; got != want {
		t.Fatalf("unexpected sidecar address: got=%s want=%s", got, want)
	}
	if got, want := cfg.Runtime.Sidecar.TargetMode, "upstream_only"; got != want {
		t.Fatalf("unexpected sidecar target_mode: got=%s want=%s", got, want)
	}
}
