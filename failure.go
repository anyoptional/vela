package jinx

import (
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
)

const failureExcerptLimit = 512

// Kind classifies a request failure.
type Kind uint8

const (
	// KindInvalidCall marks invalid request construction or policy decisions.
	KindInvalidCall Kind = iota
	// KindDiscovery marks registry failures or an empty instance pool.
	KindDiscovery
	// KindTransport marks connection, timeout, or response-read failures.
	KindTransport
	// KindStatus marks non-2xx HTTP responses.
	KindStatus
	// KindDecode marks response decoding failures.
	KindDecode
	// KindCanceled marks caller cancellation or an overall deadline.
	KindCanceled
)

// Failure carries a stable kind and the original cause of a request failure.
// Additional observability data is kept private so formatting can sanitize it.
type Failure struct {
	Kind       Kind
	StatusCode int
	Err        error

	clientName   string
	targetMode   string
	attempt      int
	attemptLimit int
	instanceID   string
	serviceID    string
	endpoint     string
	bodyExcerpt  string
}

// Error returns a bounded, sanitized failure summary without exposing the cause.
func (f *Failure) Error() string {
	if f == nil {
		return "jinx: <nil failure>"
	}

	parts := []string{"kind=" + kindName(f.Kind)}
	if f.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("status_code=%d", f.StatusCode))
	}
	if f.clientName != "" {
		parts = append(parts, "client="+strconv.Quote(f.clientName))
	}
	if f.targetMode != "" {
		parts = append(parts, "target_mode="+strconv.Quote(f.targetMode))
	}
	if f.attempt > 0 {
		parts = append(parts, fmt.Sprintf("attempt=%d", f.attempt))
	}
	if f.attemptLimit > 0 {
		parts = append(parts, fmt.Sprintf("attempt_limit=%d", f.attemptLimit))
	}
	if f.instanceID != "" {
		parts = append(parts, "instance_id="+strconv.Quote(f.instanceID))
	}
	if f.serviceID != "" {
		parts = append(parts, "service_id="+strconv.Quote(f.serviceID))
	}
	if endpoint := sanitizeEndpoint(f.endpoint); endpoint != "" {
		parts = append(parts, "endpoint="+strconv.Quote(endpoint))
	}
	if excerpt := boundedExcerpt(f.bodyExcerpt); excerpt != "" {
		parts = append(parts, "body_excerpt="+strconv.Quote(excerpt))
	}
	return "jinx: failure " + strings.Join(parts, " ")
}

// Unwrap exposes the original cause.
func (f *Failure) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.Err
}

// LogValue returns structured, sanitized observability fields.
func (f *Failure) LogValue() slog.Value {
	if f == nil {
		return slog.GroupValue(slog.String("kind", "unknown"))
	}

	attrs := []slog.Attr{slog.String("kind", kindName(f.Kind))}
	if f.StatusCode != 0 {
		attrs = append(attrs, slog.Int("status_code", f.StatusCode))
	}
	if f.clientName != "" {
		attrs = append(attrs, slog.String("client", f.clientName))
	}
	if f.targetMode != "" {
		attrs = append(attrs, slog.String("target_mode", f.targetMode))
	}
	if f.attempt > 0 {
		attrs = append(attrs, slog.Int("attempt", f.attempt))
	}
	if f.attemptLimit > 0 {
		attrs = append(attrs, slog.Int("attempt_limit", f.attemptLimit))
	}
	if f.instanceID != "" {
		attrs = append(attrs, slog.String("instance_id", f.instanceID))
	}
	if f.serviceID != "" {
		attrs = append(attrs, slog.String("service_id", f.serviceID))
	}
	if endpoint := sanitizeEndpoint(f.endpoint); endpoint != "" {
		attrs = append(attrs, slog.String("endpoint", endpoint))
	}
	if excerpt := boundedExcerpt(f.bodyExcerpt); excerpt != "" {
		attrs = append(attrs, slog.String("body_excerpt", excerpt))
	}
	return slog.GroupValue(attrs...)
}

func kindName(kind Kind) string {
	switch kind {
	case KindInvalidCall:
		return "invalid_call"
	case KindDiscovery:
		return "discovery"
	case KindTransport:
		return "transport"
	case KindStatus:
		return "status"
	case KindDecode:
		return "decode"
	case KindCanceled:
		return "canceled"
	default:
		return "unknown"
	}
}

func sanitizeEndpoint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}

func boundedExcerpt(excerpt string) string {
	return excerpt[:min(len(excerpt), failureExcerptLimit)]
}

var _ error = (*Failure)(nil)
var _ slog.LogValuer = (*Failure)(nil)
