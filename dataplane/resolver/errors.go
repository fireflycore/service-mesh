package resolver

import (
	"errors"
	"fmt"
	"strings"

	"github.com/fireflycore/service-mesh/pkg/model"
)

var ErrSnapshotDegraded = errors.New("service snapshot degraded")

type SnapshotStatusError struct {
	Target model.ServiceRef
	Status string
	Reason string
}

func (e *SnapshotStatusError) Error() string {
	if e == nil {
		return ErrSnapshotDegraded.Error()
	}
	parts := []string{ErrSnapshotDegraded.Error()}
	if strings.TrimSpace(e.Target.Service) != "" {
		parts = append(parts, fmt.Sprintf("service=%s", e.Target.Service))
	}
	if strings.TrimSpace(e.Status) != "" {
		parts = append(parts, fmt.Sprintf("status=%s", e.Status))
	}
	if strings.TrimSpace(e.Reason) != "" {
		parts = append(parts, fmt.Sprintf("reason=%s", e.Reason))
	}
	return strings.Join(parts, " ")
}

func (e *SnapshotStatusError) Unwrap() error {
	return ErrSnapshotDegraded
}
