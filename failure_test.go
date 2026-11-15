package vela

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestFailureUnwrapsCause(t *testing.T) {
	cause := errors.New("connection reset")
	failure := &Failure{
		Kind: KindTransport,
		Err:  cause,
	}

	if !errors.Is(failure, cause) {
		t.Fatal("errors.Is(failure, cause) = false, want true")
	}

	var got *Failure
	if !errors.As(failure, &got) || got != failure {
		t.Fatalf("errors.As(failure, &got) = %v, want original failure", got)
	}
}

func TestFailureErrorSanitizesEndpointAndQuery(t *testing.T) {
	cause := errors.New(`Get "https://user:password@example.com/orders?token=secret": connection reset`)
	failure := &Failure{
		Kind:       KindTransport,
		Err:        cause,
		clientName: "orders",
		targetMode: "direct",
		attempt:    2,
		endpoint:   "https://user:password@example.com/orders?token=secret",
		bodyExcerpt: strings.Repeat(
			"response-body-",
			80,
		),
	}

	message := failure.Error()
	for _, secret := range []string{"user:password", "token=secret", "connection reset"} {
		if strings.Contains(message, secret) {
			t.Errorf("Error() exposes %q: %q", secret, message)
		}
	}
	if strings.Contains(message, strings.Repeat("response-body-", 80)) {
		t.Error("Error() includes the unbounded response body excerpt")
	}
	if len(message) > 1024 {
		t.Errorf("Error() length = %d, want at most 1024", len(message))
	}
}

func TestFailureLogValueCarriesObservability(t *testing.T) {
	failure := &Failure{
		Kind:         KindStatus,
		StatusCode:   503,
		Err:          errors.New("private cause"),
		clientName:   "orders",
		targetMode:   "service",
		attempt:      2,
		attemptLimit: 3,
		instanceID:   "orders-2",
		endpoint:     "http://orders-2:8080/orders?token=hidden",
		bodyExcerpt:  strings.Repeat("x", 600),
	}

	value := failure.LogValue()
	if value.Kind() != slog.KindGroup {
		t.Fatalf("LogValue().Kind() = %v, want group", value.Kind())
	}
	attrs := make(map[string]slog.Value)
	for _, attr := range value.Group() {
		attrs[attr.Key] = attr.Value
	}

	for key, want := range map[string]string{
		"kind":        "status",
		"client":      "orders",
		"target_mode": "service",
		"instance_id": "orders-2",
		"endpoint":    "http://orders-2:8080/orders",
	} {
		got, ok := attrs[key]
		if !ok {
			t.Errorf("LogValue() missing %q", key)
			continue
		}
		if got.String() != want {
			t.Errorf("LogValue()[%q] = %q, want %q", key, got.String(), want)
		}
	}
	if got := attrs["status_code"].Int64(); got != 503 {
		t.Errorf("status_code = %d, want 503", got)
	}
	if got := attrs["attempt"].Int64(); got != 2 {
		t.Errorf("attempt = %d, want 2", got)
	}
	if got := attrs["attempt_limit"].Int64(); got != 3 {
		t.Errorf("attempt_limit = %d, want 3", got)
	}
	if got := attrs["body_excerpt"].String(); len(got) > 512 {
		t.Errorf("body_excerpt length = %d, want at most 512", len(got))
	}
	if strings.Contains(value.String(), "private cause") || strings.Contains(value.String(), "token=hidden") {
		t.Errorf("LogValue() exposes sensitive error data: %s", value)
	}
}

func TestFailureKindsExist(t *testing.T) {
	kinds := []Kind{
		KindInvalidCall,
		KindDiscovery,
		KindTransport,
		KindStatus,
		KindDecode,
		KindCanceled,
	}
	seen := make(map[Kind]struct{}, len(kinds))
	for _, kind := range kinds {
		if _, exists := seen[kind]; exists {
			t.Errorf("duplicate Failure kind value %d", kind)
		}
		seen[kind] = struct{}{}
	}
	if got, want := len(seen), 6; got != want {
		t.Errorf("unique Failure kinds = %d, want %d", got, want)
	}
}
