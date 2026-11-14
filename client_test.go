package jinx

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

type testLoadBalancer struct{}

func (testLoadBalancer) Choose(context.Context, string, []Instance) (Instance, error) {
	return Instance{}, nil
}

func TestNew_Defaults(t *testing.T) {
	client := New(nil)

	if _, ok := client.lb.(*roundRobin); !ok {
		t.Errorf("lb type = %T, want *roundRobin", client.lb)
	}
	if got, want := client.cfg.perAttemptTimeout, 5*time.Second; got != want {
		t.Errorf("perAttemptTimeout = %v, want %v", got, want)
	}
	if got, want := client.cfg.maxAttempts, 3; got != want {
		t.Errorf("maxAttempts = %d, want %d", got, want)
	}
	if got, want := client.cfg.cooldown, 10*time.Second; got != want {
		t.Errorf("cooldown = %v, want %v", got, want)
	}
	if client.logger != slog.Default() {
		t.Error("logger is not slog.Default()")
	}
	if client.httpClient == nil {
		t.Error("httpClient is nil")
	}
}

func TestClient_NewRequest(t *testing.T) {
	if request := New(nil).NewRequest("orders", http.MethodGet, "/orders"); request == nil {
		t.Fatal("NewRequest() returned nil")
	}
}

func TestNew_Options(t *testing.T) {
	lb := testLoadBalancer{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	httpClient := &http.Client{Timeout: time.Second}
	timeout := 250 * time.Millisecond
	cooldown := 2 * time.Second

	client := New(nil,
		WithLoadBalancer(lb),
		WithTimeout(timeout),
		WithMaxAttempts(7),
		WithCooldown(cooldown),
		WithLogger(logger),
		WithHTTPClient(httpClient),
	)

	if got, ok := client.lb.(testLoadBalancer); !ok || got != lb {
		t.Errorf("lb = %#v, want %#v", client.lb, lb)
	}
	if got := client.cfg.perAttemptTimeout; got != timeout {
		t.Errorf("perAttemptTimeout = %v, want %v", got, timeout)
	}
	if got := client.cfg.maxAttempts; got != 7 {
		t.Errorf("maxAttempts = %d, want %d", got, 7)
	}
	if got := client.cfg.cooldown; got != cooldown {
		t.Errorf("cooldown = %v, want %v", got, cooldown)
	}
	if client.logger != logger {
		t.Error("logger was not applied")
	}
	if client.httpClient != httpClient {
		t.Error("httpClient was not applied")
	}
}

func TestNew_InvalidOptionsFallback(t *testing.T) {
	client := New(nil,
		WithTimeout(-time.Second),
		WithMaxAttempts(0),
		WithCooldown(-time.Second),
	)

	if got, want := client.cfg.perAttemptTimeout, 5*time.Second; got != want {
		t.Errorf("perAttemptTimeout = %v, want %v", got, want)
	}
	if got, want := client.cfg.maxAttempts, 3; got != want {
		t.Errorf("maxAttempts = %d, want %d", got, want)
	}
	if got := client.cfg.cooldown; got != 0 {
		t.Errorf("cooldown = %v, want 0", got)
	}

	zeroCooldown := New(nil, WithCooldown(0))
	if got, want := zeroCooldown.cfg.cooldown, 10*time.Second; got != want {
		t.Errorf("WithCooldown(0) cooldown = %v, want %v", got, want)
	}
}
