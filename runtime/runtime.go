package runtime

import "context"

// Runner 是 agent / sidecar 共享的最小运行时抽象。
type Runner interface {
	Run(ctx context.Context) error
}
