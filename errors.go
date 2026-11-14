package jinx

import "errors"

// ErrNoInstances indicates that no candidate instances are available.
var ErrNoInstances = errors.New("jinx: no instances available")

// TransportError marks a failure that occurred while sending an HTTP request.
type TransportError struct {
	Err error
}

func (e *TransportError) Error() string {
	if e.Err == nil {
		return "jinx: transport: <nil>"
	}
	return "jinx: transport: " + e.Err.Error()
}

func (e *TransportError) Unwrap() error {
	return e.Err
}
