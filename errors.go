package jinx

import "errors"

// ErrNoInstances indicates that no candidate instances are available.
var ErrNoInstances = errors.New("jinx: no instances available")
