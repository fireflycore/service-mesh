package runtime

import "context"

// Runner 是 agent / sidecar 共享的最小运行时抽象。
type Runner interface {
	// Run 会阻塞直到运行时退出或 ctx 被取消。
	//
	// 调用方通常只需要关心两件事：
	// - 返回 nil：表示按预期退出
	// - 返回 error：表示启动或运行过程中出现了异常
	Run(ctx context.Context) error
}
