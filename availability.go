package vela

// DefaultAvailability reports whether a failure indicates a likely availability problem.
func DefaultAvailability(failure *Failure) bool {
	if failure == nil {
		return false
	}

	switch failure.Kind {
	case KindDiscovery, KindTransport:
		return true
	case KindStatus:
		return failure.StatusCode >= 500
	default:
		return false
	}
}
