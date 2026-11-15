package vela

import "errors"

// ErrNoInstances indicates that no candidate instances are available.
var ErrNoInstances = errors.New("vela: no instances available")
