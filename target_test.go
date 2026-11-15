package jinx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNew_RequiresTargetName(t *testing.T) {
	client, err := New(Target{}, WithDiscovery(fakeDiscovery{}))
	if err == nil {
		t.Fatal("New() error = nil, want missing target name error")
	}
	if client != nil {
		t.Fatalf("New() client = %p, want nil for invalid target", client)
	}
	var failure *Failure
	if errors.As(err, &failure) {
		t.Fatalf("New() error = %T, want ordinary validation error", err)
	}
}

func TestNew_ServiceTargetRequiresDiscovery(t *testing.T) {
	client, err := New(Target{Name: "orders"})
	if err == nil {
		t.Fatal("New() error = nil, want missing discovery error")
	}
	if client != nil {
		t.Fatalf("New() client = %p, want nil without discovery", client)
	}
}

func TestNew_DirectTargetDoesNotRequireDiscovery(t *testing.T) {
	client, err := New(Target{Name: "orders", URL: "http://127.0.0.1:8080"})
	if err != nil {
		t.Fatalf("New() error = %v, want direct target without discovery", err)
	}
	if client == nil {
		t.Fatal("New() client = nil")
	}
}

func TestDirectTarget_NeverCallsDiscoveryOrLoadBalancer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	discoveryCalls := 0
	discovery := fakeDiscovery{
		instances: []Instance{{ID: "orders-1", Host: "unused", Port: 8080}},
		calls:     &discoveryCalls,
	}
	loadBalancer := &countingLoadBalancer{}
	client, err := New(
		Target{Name: "orders", URL: server.URL},
		WithDiscovery(discovery),
		WithLoadBalancer(loadBalancer),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := client.NewRequest(http.MethodGet, "/health").Do(t.Context()); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if discoveryCalls != 0 {
		t.Errorf("discovery calls = %d, want 0", discoveryCalls)
	}
	if loadBalancer.calls != 0 {
		t.Errorf("load-balancer calls = %d, want 0", loadBalancer.calls)
	}
}

func TestDirectTarget_RejectsMalformedAndUnsupportedURLs(t *testing.T) {
	tests := []struct {
		name   string
		target Target
	}{
		{name: "malformed", target: Target{Name: "orders", URL: "http://%zz"}},
		{name: "missing host", target: Target{Name: "orders", URL: "https://"}},
		{name: "unsupported scheme", target: Target{Name: "orders", URL: "ftp://example.com"}},
		{name: "URL query", target: Target{Name: "orders", URL: "https://example.com/api?token=secret"}},
		{name: "URL fragment", target: Target{Name: "orders", URL: "https://example.com/api#part"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(tt.target)
			if err == nil {
				t.Fatal("New() error = nil, want target validation error")
			}
			if client != nil {
				t.Fatalf("New() client = %p, want nil for invalid target", client)
			}
			var failure *Failure
			if errors.As(err, &failure) {
				t.Fatalf("New() error = %T, want ordinary validation error", err)
			}
		})
	}
}

func TestDirectTarget_JoinsBaseRoutePathAndEscapedParams(t *testing.T) {
	requestURI := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI = r.RequestURI
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	client, err := New(Target{
		Name: "orders",
		URL:  server.URL + "/base%20path",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.NewRequest(http.MethodGet, "/orders/:id").
		SetPathParam("id", "a/b").
		SetQueryParam("expand", "line items").
		Do(t.Context())
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if want := "/base%20path/orders/a%2Fb?expand=line+items"; requestURI != want {
		t.Errorf("request URI = %q, want %q", requestURI, want)
	}
}

func TestRequest_MissingPathParamFailsBeforeNetwork(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	client, err := New(Target{Name: "orders", URL: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = client.NewRequest(http.MethodGet, "/orders/:id").Do(t.Context())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != KindInvalidCall ||
		!strings.Contains(failure.Err.Error(), `missing path param "id"`) {
		t.Fatalf("Do() error = %v, want missing path parameter error", err)
	}
	if requests != 0 {
		t.Errorf("HTTP requests = %d, want 0", requests)
	}
}

type countingLoadBalancer struct {
	calls int
}

func (lb *countingLoadBalancer) Choose(_ context.Context, _ string, candidates []Instance) (Instance, error) {
	lb.calls++
	if len(candidates) == 0 {
		return Instance{}, ErrNoInstances
	}
	return candidates[0], nil
}
