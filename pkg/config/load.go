package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// LoadOptions 表示 CLI 与外部调用者允许覆盖的最小配置入口。
type LoadOptions struct {
	Path               string
	Mode               string
	SourceKind         string
	AuthzTarget        string
	ControlPlaneTarget string
}

// Load 按固定顺序组装最终配置：
// 1. 默认值
// 2. 文件配置
// 3. 环境变量覆盖
// 4. CLI 覆盖
// 5. Normalize + Validate
func Load(opts LoadOptions) (*Config, error) {
	cfg := Default()

	if opts.Path != "" {
		raw, err := os.ReadFile(opts.Path)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return nil, err
		}
	}

	applyEnv(&cfg)
	applyOptions(&cfg, opts)
	Normalize(&cfg)

	if err := Validate(cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Render 用于把最终配置重新输出成 YAML。
func Render(cfg Config) ([]byte, error) {
	return yaml.Marshal(cfg)
}

// applyEnv 把环境变量映射到运行时配置。
func applyEnv(cfg *Config) {
	if v := os.Getenv("SERVICE_MESH_MODE"); v != "" {
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
