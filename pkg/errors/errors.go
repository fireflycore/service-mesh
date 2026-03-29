package serrs

import "errors"

// 这些错误值用于让上层可以区分“模式不支持”和“来源不支持”这两类配置错误。
var (
	ErrInvalidMode   = errors.New("invalid mode")
	ErrInvalidSource = errors.New("invalid source")
)
