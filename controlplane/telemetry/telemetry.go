package telemetry

import (
	"context"
	"strings"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/controlplane/snapshot"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type Emitter struct {
	watchRestartCounter metric.Int64Counter
	watchUpdateCounter  metric.Int64Counter
}

func NewEmitter() *Emitter {
	meter := otel.Meter("github.com/fireflycore/service-mesh/controlplane/telemetry")

	watchRestartCounter, _ := meter.Int64Counter("service_mesh.controlplane.watch.restarts")
	watchUpdateCounter, _ := meter.Int64Counter("service_mesh.controlplane.watch.updates")

	return &Emitter{
		watchRestartCounter: watchRestartCounter,
		watchUpdateCounter:  watchUpdateCounter,
	}
}

func (e *Emitter) RecordWatchRestart(ctx context.Context, target string) {
	if e == nil || e.watchRestartCounter == nil {
		return
	}
	e.watchRestartCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("mesh.target.service", target),
	))
}

func (e *Emitter) RecordWatchUpdate(ctx context.Context, update snapshot.WatchUpdate) {
	if e == nil || e.watchUpdateCounter == nil {
		return
	}

	status := "deleted"
	reasonClass := ""
	if update.Snapshot != nil {
		status = snapshotStatusLabel(update.Snapshot.GetStatus())
		reasonClass = snapshotReasonClass(update.Snapshot.GetStatusReason())
	}

	e.watchUpdateCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("mesh.snapshot.status", status),
		attribute.String("mesh.snapshot.reason_class", reasonClass),
	))
}

func snapshotStatusLabel(status controlv1.SnapshotStatus) string {
	switch status {
	case controlv1.SnapshotStatus_SNAPSHOT_STATUS_STALE:
		return "stale"
	case controlv1.SnapshotStatus_SNAPSHOT_STATUS_DEGRADED:
		return "degraded"
	case controlv1.SnapshotStatus_SNAPSHOT_STATUS_CURRENT:
		return "current"
	default:
		return "unspecified"
	}
}

func snapshotReasonClass(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return ""
	}
	const prefix = "class="
	if !strings.HasPrefix(trimmed, prefix) {
		return ""
	}
	remainder := strings.TrimPrefix(trimmed, prefix)
	if idx := strings.IndexByte(remainder, ' '); idx >= 0 {
		return remainder[:idx]
	}
	return remainder
}
