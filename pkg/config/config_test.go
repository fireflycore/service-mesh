package config

import "testing"

func TestDefaultConfigIsValid(t *testing.T) {
	cfg := Default()
	Normalize(&cfg)

	if err := Validate(cfg); err != nil {
		t.Fatalf("expected default config to be valid: %v", err)
	}
}

func TestInvalidModeFails(t *testing.T) {
	cfg := Default()
	cfg.Mode = "bad"

	if err := Validate(cfg); err == nil {
		t.Fatal("expected invalid mode to fail")
	}
}

func TestInvokePerTryTimeoutCannotExceedTimeout(t *testing.T) {
	cfg := Default()
	cfg.Invoke.TimeoutMS = 100
	cfg.Invoke.PerTryTimeoutMS = 200

	if err := Validate(cfg); err == nil {
		t.Fatal("expected invalid invoke timeout config to fail")
	}
}

func TestSidecarRequiresServiceName(t *testing.T) {
	cfg := Default()
	cfg.Mode = "sidecar"
	cfg.Runtime.Sidecar.ServiceName = ""

	if err := Validate(cfg); err == nil {
		t.Fatal("expected sidecar without service_name to fail")
	}
}
