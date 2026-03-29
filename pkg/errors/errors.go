package serrs

import "errors"

// 这些错误值用于让上层可以区分“模式不支持”和“来源不支持”这两类配置错误。
var (
	// ErrInvalidMode 用于包装 mode 配置不合法的错误。
	ErrInvalidMode = errors.New("invalid mode")
	// ErrInvalidSource 用于包装 source.kind 不合法的错误。
	ErrInvalidSource = errors.New("invalid source")
)
