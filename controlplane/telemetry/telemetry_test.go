package telemetry

import (
	"testing"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
)

func TestSnapshotReasonClass(t *testing.T) {
	if got, want := snapshotReasonClass("class=timeout error=context deadline exceeded"), "timeout"; got != want {
		t.Fatalf("unexpected reason class: got=%s want=%s", got, want)
	}
	if got := snapshotReasonClass("registry unavailable"); got != "" {
		t.Fatalf("expected empty reason class for raw error, got=%s", got)
	}
}

func TestSnapshotStatusLabel(t *testing.T) {
	if got, want := snapshotStatusLabel(controlv1.SnapshotStatus_SNAPSHOT_STATUS_STALE), "stale"; got != want {
		t.Fatalf("unexpected stale label: got=%s want=%s", got, want)
	}
	if got, want := snapshotStatusLabel(controlv1.SnapshotStatus_SNAPSHOT_STATUS_DEGRADED), "degraded"; got != want {
		t.Fatalf("unexpected degraded label: got=%s want=%s", got, want)
	}
}

func TestSnapshotReasonClassEmpty(t *testing.T) {
	if got, want := snapshotReasonClass("class=empty error=no healthy source endpoints: etcd service=orders"), "empty"; got != want {
		t.Fatalf("unexpected empty reason class: got=%s want=%s", got, want)
	}
}
