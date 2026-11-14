package jinx

import (
	"errors"
	"testing"
)

func TestTransportError_FormatsAndUnwraps(t *testing.T) {
	cause := errors.New("connection reset")
	err := &TransportError{Err: cause}

	if got, want := err.Error(), "jinx: transport: connection reset"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(%v, cause) = false, want true", err)
	}
}

func TestTransportError_NilCause(t *testing.T) {
	err := &TransportError{}

	if got, want := err.Error(), "jinx: transport: <nil>"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
