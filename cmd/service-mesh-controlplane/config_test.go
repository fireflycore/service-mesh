package main

import "testing"

func TestBootstrapStore(t *testing.T) {
	store := bootstrapStore(controlPlaneConfig{
		Snapshots: []snapshotConfig{
			{
				Service: serviceRefConfig{Service: "orders", Namespace: "default", Env: "dev"},
				Status:  "current",
				Endpoints: []endpointConfig{
					{Address: "127.0.0.1", Port: 50051, Weight: 1},
				},
			},
		},
		RoutePolicies: []routePolicyConfig{
			{
				Service:   serviceRefConfig{Service: "orders", Namespace: "default", Env: "dev"},
				TimeoutMS: 1500,
				Retry: retryPolicyConfig{
					MaxAttempts:     2,
					PerTryTimeoutMS: 500,
				},
			},
		},
	})

	if got, want := len(store.AllServiceSnapshots()), 1; got != want {
		t.Fatalf("unexpected snapshot count: got=%d want=%d", got, want)
	}
	if got, want := len(store.AllRoutePolicies()), 1; got != want {
		t.Fatalf("unexpected route policy count: got=%d want=%d", got, want)
	}
}
