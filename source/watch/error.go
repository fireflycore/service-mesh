package watch

import "errors"

// ErrNoHealthyEndpoints 表示 provider 虽然找到了服务，但已经没有健康实例可用。
var ErrNoHealthyEndpoints = errors.New("no healthy source endpoints")
