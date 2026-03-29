package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type LoadOptions struct {
	Path               string
	Mode               string
	SourceKind         string
	AuthzTarget        string
	ControlPlaneTarget string
}

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

func Render(cfg Config) ([]byte, error) {
	return yaml.Marshal(cfg)
}

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
