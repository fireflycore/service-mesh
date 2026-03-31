package sourceerr

import "errors"

var ErrNoHealthyEndpoints = errors.New("no healthy source endpoints")
