package main

import (
	"os"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/plane/control/snapshot"
	"gopkg.in/yaml.v3"
)

type controlPlaneConfig struct {
	Listen         string              `yaml:"listen"`
	Snapshots      []snapshotConfig    `yaml:"snapshots"`
	RoutePolicies  []routePolicyConfig `yaml:"route_policies"`
	TrackedTargets []serviceRefConfig  `yaml:"tracked_targets"`
}

type serviceRefConfig struct {
	Service   string `yaml:"service"`
	Namespace string `yaml:"namespace"`
	Env       string `yaml:"env"`
	Port      uint32 `yaml:"port"`
}

type endpointConfig struct {
	Address string `yaml:"address"`
	Port    uint32 `yaml:"port"`
	Weight  uint32 `yaml:"weight"`
}

type snapshotConfig struct {
	Service      serviceRefConfig `yaml:"service"`
	Revision     string           `yaml:"revision"`
	Status       string           `yaml:"status"`
	StatusReason string           `yaml:"status_reason"`
	Endpoints    []endpointConfig `yaml:"endpoints"`
}

type retryPolicyConfig struct {
	MaxAttempts     uint32 `yaml:"max_attempts"`
	PerTryTimeoutMS uint32 `yaml:"per_try_timeout_ms"`
}

type routePolicyConfig struct {
	Service   serviceRefConfig  `yaml:"service"`
	TimeoutMS uint32            `yaml:"timeout_ms"`
	Retry     retryPolicyConfig `yaml:"retry"`
}

func loadControlPlaneConfig(path string) (controlPlaneConfig, error) {
	var cfg controlPlaneConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:19080"
	}
	return cfg, nil
}

func bootstrapStore(cfg controlPlaneConfig) *snapshot.Store {
	store := snapshot.NewStore()
	for _, item := range cfg.Snapshots {
		store.PutServiceSnapshot(&controlv1.ServiceSnapshot{
			Service:      toProtoService(item.Service),
			Revision:     item.Revision,
			Status:       toProtoStatus(item.Status),
			StatusReason: item.StatusReason,
			Endpoints:    toProtoEndpoints(item.Endpoints),
		})
	}
	for _, item := range cfg.RoutePolicies {
		store.PutRoutePolicy(&controlv1.RoutePolicy{
			Service:   toProtoService(item.Service),
			TimeoutMs: uint64(item.TimeoutMS),
			Retry: &controlv1.RetryPolicy{
				MaxAttempts:     item.Retry.MaxAttempts,
				PerTryTimeoutMs: uint64(item.Retry.PerTryTimeoutMS),
			},
		})
	}
	return store
}

func toModelService(ref serviceRefConfig) model.ServiceRef {
	return model.ServiceRef{
		Service:   ref.Service,
		Namespace: ref.Namespace,
		Env:       ref.Env,
		Port:      ref.Port,
	}
}

func toProtoService(ref serviceRefConfig) *controlv1.ServiceRef {
	return &controlv1.ServiceRef{
		Service:   ref.Service,
		Namespace: ref.Namespace,
		Env:       ref.Env,
		Port:      ref.Port,
	}
}

func toProtoEndpoints(items []endpointConfig) []*controlv1.Endpoint {
	if len(items) == 0 {
		return nil
	}
	endpoints := make([]*controlv1.Endpoint, 0, len(items))
	for _, item := range items {
		endpoints = append(endpoints, &controlv1.Endpoint{
			Address: item.Address,
			Port:    item.Port,
			Weight:  item.Weight,
		})
	}
	return endpoints
}

func toProtoStatus(status string) controlv1.SnapshotStatus {
	switch status {
	case model.SnapshotStatusStale:
		return controlv1.SnapshotStatus_SNAPSHOT_STATUS_STALE
	case model.SnapshotStatusDegraded:
		return controlv1.SnapshotStatus_SNAPSHOT_STATUS_DEGRADED
	default:
		return controlv1.SnapshotStatus_SNAPSHOT_STATUS_CURRENT
	}
}
