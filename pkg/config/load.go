package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// LoadOptions 表示 CLI 与外部调用者允许覆盖的最小配置入口。
type LoadOptions struct {
	// Path 指向 YAML 配置文件；为空时直接从默认配置开始。
	Path string
	// Mode 允许通过 CLI 强制切换运行模式。
	Mode string
	// SourceKind 允许通过 CLI 快速切换目录来源。
	SourceKind string
	// AuthzTarget 允许在联调时临时替换鉴权地址。
	AuthzTarget string
	// ControlPlaneTarget 允许在联调时临时替换控制面地址。
	ControlPlaneTarget string
}

// Load 按固定顺序组装最终配置：
// 1. 默认值
// 2. 文件配置
// 3. 环境变量覆盖
// 4. CLI 覆盖
// 5. Normalize + Validate
func Load(opts LoadOptions) (*Config, error) {
	// 先拿一份完整默认值，保证即使没有配置文件也有可运行基线。
	cfg := Default()

	if opts.Path != "" {
		// 文件配置只覆盖显式声明的字段，未声明字段继续保留默认值。
		raw, err := os.ReadFile(opts.Path)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return nil, err
		}
	}

	// 环境变量和 CLI 覆盖放在文件配置之后，确保本地联调时优先级更高。
	applyEnv(&cfg)
	applyOptions(&cfg, opts)
	// Normalize 负责“修整 + 补默认值”，让 Validate 只聚焦合法性判断。
	Normalize(&cfg)

	if err := Validate(cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Render 用于把最终配置重新输出成 YAML。
func Render(cfg Config) ([]byte, error) {
	// Render 主要给 print-config 之类的可视化场景使用。
	return yaml.Marshal(cfg)
}

// applyEnv 把环境变量映射到运行时配置。
func applyEnv(cfg *Config) {
	if v := os.Getenv("SERVICE_MESH_MODE"); v != "" {
		// 环境变量覆盖适合容器部署或 CI 场景下做轻量配置替换。
		cfg.Mode = v
	}
	if v := os.Getenv("SERVICE_MESH_SOURCE_KIND"); v != "" {
		cfg.Source.Kind = v
	}
	if v := os.Getenv("SERVICE_MESH_AUTHZ_TARGET"); v != "" {
		cfg.Authz.Target = v
	}
	if v := os.Getenv("SERVICE_MESH_CONTROLPLANE_TARGET"); v != "" {
		cfg.ControlPlane.Target = v
	}
}

// applyOptions 把 CLI 参数覆盖到配置对象。
func applyOptions(cfg *Config, opts LoadOptions) {
	// CLI 覆盖优先级最高，便于本地临时联调。
	if opts.Mode != "" {
		cfg.Mode = opts.Mode
	}
	if opts.SourceKind != "" {
		cfg.Source.Kind = opts.SourceKind
	}
	if opts.AuthzTarget != "" {
		cfg.Authz.Target = opts.AuthzTarget
	}
	if opts.ControlPlaneTarget != "" {
		cfg.ControlPlane.Target = opts.ControlPlaneTarget
	}
}
