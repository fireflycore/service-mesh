package serrs

import "errors"

var (
	ErrInvalidMode   = errors.New("invalid mode")
	ErrInvalidSource = errors.New("invalid source")
)
