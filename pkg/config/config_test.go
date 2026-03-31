package config

import (
	"testing"

	"github.com/fireflycore/service-mesh/pkg/model"
)

// TestDefaultConfigIsValid 验证默认配置经过规范化后可以直接运行。
func TestDefaultConfigIsValid(t *testing.T) {
	cfg := Default()
	// 先走 Normalize，确保测试和真实加载链路一致。
	Normalize(&cfg)

	if err := Validate(cfg); err != nil {
		t.Fatalf("expected default config to be valid: %v", err)
	}
}

// TestInvalidModeFails 验证非法 mode 会被拦截。
func TestInvalidModeFails(t *testing.T) {
	cfg := Default()
	// 故意写一个不存在的模式名，验证 mode 校验分支。
	cfg.Mode = "bad"

	if err := Validate(cfg); err == nil {
		t.Fatal("expected invalid mode to fail")
	}
}

// TestInvokePerTryTimeoutCannotExceedTimeout 验证调用预算约束有效。
func TestInvokePerTryTimeoutCannotExceedTimeout(t *testing.T) {
	cfg := Default()
	// 构造一个前后矛盾的调用预算组合。
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
	// sidecar 身份缺 service_name 时，后续 controlplane register 和日志身份都会失真。
	cfg.Runtime.Sidecar.ServiceName = ""

	if err := Validate(cfg); err == nil {
		t.Fatal("expected sidecar without service_name to fail")
	}
}

func TestSidecarRejectsInvalidTargetMode(t *testing.T) {
	cfg := Default()
	cfg.Mode = "sidecar"
	cfg.Runtime.Sidecar.TargetMode = "bad"

	if err := Validate(cfg); err == nil {
		t.Fatal("expected sidecar with invalid target_mode to fail")
	}
}

func TestNormalizeDefaultsSidecarTargetMode(t *testing.T) {
	cfg := Default()
	cfg.Runtime.Sidecar.TargetMode = ""

	Normalize(&cfg)

	if got, want := cfg.Runtime.Sidecar.TargetMode, model.SidecarTargetModeUpstreamOnly; got != want {
		t.Fatalf("unexpected target_mode: got=%s want=%s", got, want)
	}
}

func TestValidateAcceptsCrossScopeSameServiceTargetMode(t *testing.T) {
	cfg := Default()
	cfg.Mode = model.ModeSidecar
	cfg.Runtime.Sidecar.TargetMode = model.SidecarTargetModeAllowCrossScopeSameService

	if err := Validate(cfg); err != nil {
		t.Fatalf("expected cross-scope same-service target_mode to be valid: %v", err)
	}
}

func TestNormalizeDefaultsConsulQueryTimeout(t *testing.T) {
	cfg := Default()
	cfg.Source.Consul.QueryTimeoutMS = 0

	Normalize(&cfg)

	if got, want := cfg.Source.Consul.QueryTimeoutMS, uint64(1000); got != want {
		t.Fatalf("unexpected consul query timeout: got=%d want=%d", got, want)
	}
}

func TestNormalizeDefaultsEtcdQueryTimeout(t *testing.T) {
	cfg := Default()
	cfg.Source.Etcd.QueryTimeoutMS = 0

	Normalize(&cfg)

	if got, want := cfg.Source.Etcd.QueryTimeoutMS, uint64(1000); got != want {
		t.Fatalf("unexpected etcd query timeout: got=%d want=%d", got, want)
	}
}

func TestNormalizeDefaultsConsulWatchDegradeAfterErrors(t *testing.T) {
	cfg := Default()
	cfg.Source.Consul.WatchDegradeAfterErrors = 0

	Normalize(&cfg)

	if got, want := cfg.Source.Consul.WatchDegradeAfterErrors, uint32(3); got != want {
		t.Fatalf("unexpected consul watch degrade threshold: got=%d want=%d", got, want)
	}
}

func TestNormalizeDefaultsEtcdWatchDegradeAfterErrors(t *testing.T) {
	cfg := Default()
	cfg.Source.Etcd.WatchDegradeAfterErrors = 0

	Normalize(&cfg)

	if got, want := cfg.Source.Etcd.WatchDegradeAfterErrors, uint32(3); got != want {
		t.Fatalf("unexpected etcd watch degrade threshold: got=%d want=%d", got, want)
	}
}

func TestDefaultConfigDisablesSourceFallbackWhenControlPlaneEnabled(t *testing.T) {
	cfg := Default()

	if cfg.ControlPlane.AllowSourceFallback {
		t.Fatal("expected controlplane source fallback to be disabled by default")
	}
}
