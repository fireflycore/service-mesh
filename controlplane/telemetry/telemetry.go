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
	replayCounter       metric.Int64Counter
	pushDecisionCounter metric.Int64Counter
}

func NewEmitter() *Emitter {
	meter := otel.Meter("github.com/fireflycore/service-mesh/controlplane/telemetry")

	watchRestartCounter, _ := meter.Int64Counter("service_mesh.controlplane.watch.restarts")
	watchUpdateCounter, _ := meter.Int64Counter("service_mesh.controlplane.watch.updates")
	replayCounter, _ := meter.Int64Counter("service_mesh.controlplane.replay.resources")
	pushDecisionCounter, _ := meter.Int64Counter("service_mesh.controlplane.push.decisions")

	return &Emitter{
		watchRestartCounter: watchRestartCounter,
		watchUpdateCounter:  watchUpdateCounter,
		replayCounter:       replayCounter,
		pushDecisionCounter: pushDecisionCounter,
	}
}

func (e *Emitter) RecordWatchRestart(ctx context.Context, provider, service, namespace, env string) {
	if e == nil || e.watchRestartCounter == nil {
		return
	}
	e.watchRestartCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("mesh.source.provider", provider),
		attribute.String("mesh.target.service", service),
		attribute.String("mesh.target.namespace", namespace),
		attribute.String("mesh.target.env", env),
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
		reasonClass = SnapshotReasonClass(update.Snapshot.GetStatusReason())
	}

	e.watchUpdateCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("mesh.source.provider", update.Provider),
		attribute.String("mesh.target.service", update.Target.Service),
		attribute.String("mesh.target.namespace", update.Target.Namespace),
		attribute.String("mesh.target.env", update.Target.Env),
		attribute.String("mesh.snapshot.status", status),
		attribute.String("mesh.snapshot.reason_class", reasonClass),
	))
}

func (e *Emitter) RecordReplayResource(ctx context.Context, phase, dataplaneID, namespace, env, resourceKind, matchKind string, count int64) {
	if e == nil || e.replayCounter == nil || count <= 0 {
		return
	}
	e.replayCounter.Add(ctx, count, metric.WithAttributes(
		attribute.String("mesh.replay.phase", phase),
		attribute.String("mesh.dataplane.id", dataplaneID),
		attribute.String("mesh.dataplane.namespace", namespace),
		attribute.String("mesh.dataplane.env", env),
		attribute.String("mesh.resource.kind", resourceKind),
		attribute.String("mesh.match.kind", matchKind),
	))
}

func (e *Emitter) RecordPushDecision(ctx context.Context, responseKind, service, namespace, env, decision, subscriptionMatch, identityMatch string, count int64) {
	if e == nil || e.pushDecisionCounter == nil || count <= 0 {
		return
	}
	e.pushDecisionCounter.Add(ctx, count, metric.WithAttributes(
		attribute.String("mesh.response.kind", responseKind),
		attribute.String("mesh.target.service", service),
		attribute.String("mesh.target.namespace", namespace),
		attribute.String("mesh.target.env", env),
		attribute.String("mesh.push.decision", decision),
		attribute.String("mesh.subscription.match", subscriptionMatch),
		attribute.String("mesh.identity.match", identityMatch),
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

func SnapshotReasonClass(reason string) string {
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
