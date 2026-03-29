package config

import "testing"

// TestDefaultConfigIsValid 验证默认配置经过规范化后可以直接运行。
func TestDefaultConfigIsValid(t *testing.T) {
	cfg := Default()
	Normalize(&cfg)

	if err := Validate(cfg); err != nil {
		t.Fatalf("expected default config to be valid: %v", err)
	}
}

// TestInvalidModeFails 验证非法 mode 会被拦截。
func TestInvalidModeFails(t *testing.T) {
	cfg := Default()
	cfg.Mode = "bad"

	if err := Validate(cfg); err == nil {
		t.Fatal("expected invalid mode to fail")
	}
}

// TestInvokePerTryTimeoutCannotExceedTimeout 验证调用预算约束有效。
func TestInvokePerTryTimeoutCannotExceedTimeout(t *testing.T) {
	cfg := Default()
	cfg.Invoke.TimeoutMS = 100
	cfg.Invoke.PerTryTimeoutMS = 200

	if err := Validate(cfg); err == nil {
		t.Fatal("expected invalid invoke timeout config to fail")
	}
}

// TestSidecarRequiresServiceName 验证 sidecar 必须显式绑定本地服务名。
func TestSidecarRequiresServiceName(t *testing.T) {
	cfg := Default()
	cfg.Mode = "sidecar"
	cfg.Runtime.Sidecar.ServiceName = ""

	if err := Validate(cfg); err == nil {
		t.Fatal("expected sidecar without service_name to fail")
	}
}
