package jinx

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
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

type fakeDiscovery struct {
	instances []Instance
	err       error
}

func (d fakeDiscovery) Description() string {
	return "fake"
}

func (d fakeDiscovery) Instances(context.Context, string) ([]Instance, error) {
	return d.instances, d.err
}

func (fakeDiscovery) Services(context.Context) ([]string, error) {
	return nil, nil
}

type fakeTransportResult struct {
	statusCode int
	body       string
	err        error
}

type fakeTransport struct {
	mu      sync.Mutex
	results map[string][]fakeTransportResult
	next    map[string]int
	hosts   []string
	urls    []string
}

func (f *fakeTransport) do(_ context.Context, req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	host := req.URL.Hostname()
	f.hosts = append(f.hosts, host)
	f.urls = append(f.urls, req.URL.String())
	resultIndex := f.next[host]
	f.next[host]++
	results := f.results[host]
	result := fakeTransportResult{statusCode: http.StatusOK}
	if resultIndex < len(results) {
		result = results[resultIndex]
	}
	f.mu.Unlock()

	if result.err != nil {
		return nil, result.err
	}
	return &http.Response{
		StatusCode: result.statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(result.body)),
		Request:    req,
	}, nil
}

func (f *fakeTransport) snapshot() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.hosts...), append([]string(nil), f.urls...)
}

func newFailoverTestClient(discovery DiscoveryClient, transport *fakeTransport, opts ...Option) *Client {
	client := New(discovery, opts...)
	client.transport = transport.do
	return client
}

type firstLoadBalancer struct{}

func (firstLoadBalancer) Choose(_ context.Context, _ string, candidates []Instance) (Instance, error) {
	if len(candidates) == 0 {
		return Instance{}, ErrNoInstances
	}
	return candidates[0], nil
}

func TestDo_FirstAttemptSuccess(t *testing.T) {
	discovery := fakeDiscovery{instances: []Instance{{ID: "a", Host: "a", Port: 8080}}}
	transport := &fakeTransport{results: map[string][]fakeTransportResult{
		"a": {{statusCode: http.StatusOK, body: `{"id":"42","name":"desk"}`}},
	}, next: make(map[string]int)}
	client := newFailoverTestClient(discovery, transport)
	var result struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	response, err := client.NewRequest("orders", http.MethodGet, "/orders/42").
		SetResult(&result).
		Do(t.Context())
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if response == nil || response.StatusCode != http.StatusOK {
		t.Fatalf("Do() response = %#v, want status %d", response, http.StatusOK)
	}
	if got, want := string(response.Body), `{"id":"42","name":"desk"}`; got != want {
		t.Errorf("Response.Body = %q, want %q", got, want)
	}
	if result.ID != "42" || result.Name != "desk" {
		t.Errorf("decoded result = %#v", result)
	}
}

func TestFailover_NextInstanceOn5xx(t *testing.T) {
	discovery := fakeDiscovery{instances: []Instance{
		{ID: "a", Host: "a", Port: 8080},
		{ID: "b", Host: "b", Port: 8080},
	}}
	transport := &fakeTransport{results: map[string][]fakeTransportResult{
		"a": {{statusCode: http.StatusInternalServerError, body: "down"}},
		"b": {{statusCode: http.StatusOK, body: "ok"}},
	}, next: make(map[string]int)}
	client := newFailoverTestClient(discovery, transport)

	response, err := client.NewRequest("orders", http.MethodGet, "/orders").Do(t.Context())
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	hosts, _ := transport.snapshot()
	if !reflect.DeepEqual(hosts, []string{"a", "b"}) {
		t.Errorf("transport hosts = %v, want [a b]", hosts)
	}
	if response == nil || response.StatusCode != http.StatusOK {
		t.Errorf("Do() response = %#v, want status %d", response, http.StatusOK)
	}
}

func TestFailover_NoFailoverOn4xx(t *testing.T) {
	body := strings.Repeat("x", 512) + "TAIL"
	discovery := fakeDiscovery{instances: []Instance{
		{ID: "a", Host: "a", Port: 8080},
		{ID: "b", Host: "b", Port: 8080},
	}}
	transport := &fakeTransport{results: map[string][]fakeTransportResult{
		"a": {{statusCode: http.StatusNotFound, body: body}},
	}, next: make(map[string]int)}
	client := newFailoverTestClient(discovery, transport)

	response, err := client.NewRequest("orders", http.MethodGet, "/orders").Do(t.Context())
	if err == nil {
		t.Fatal("Do() error = nil, want status error")
	}
	if response == nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("Do() response = %#v, want status %d", response, http.StatusNotFound)
	}
	if got := string(response.Body); got != body {
		t.Errorf("Response.Body length = %d, want %d", len(got), len(body))
	}
	if !strings.Contains(err.Error(), body[:512]) || strings.Contains(err.Error(), "TAIL") {
		t.Errorf("status error has wrong body excerpt: %q", err)
	}
	hosts, _ := transport.snapshot()
	if len(hosts) != 1 {
		t.Errorf("transport calls = %d, want 1", len(hosts))
	}
}

func TestFailover_TransportErrorCooldown(t *testing.T) {
	now := time.Now()
	discovery := fakeDiscovery{instances: []Instance{
		{ID: "a", Host: "a", Port: 8080},
		{ID: "b", Host: "b", Port: 8080},
	}}
	transport := &fakeTransport{results: map[string][]fakeTransportResult{
		"a": {
			{err: errors.New("connection failed")},
			{statusCode: http.StatusOK, body: "recovered"},
		},
	}, next: make(map[string]int)}
	client := newFailoverTestClient(discovery, transport,
		WithLoadBalancer(firstLoadBalancer{}),
		WithCooldown(time.Minute),
	)
	client.cool.now = func() time.Time { return now }

	for range 2 {
		if _, err := client.NewRequest("orders", http.MethodGet, "/orders").Do(t.Context()); err != nil {
			t.Fatalf("Do() error = %v", err)
		}
	}
	if client.cool.isCooling(discovery.instances[0]) != true {
		t.Fatal("instance a is not cooling after transport error")
	}
	if got := client.cool.len(); got != 1 {
		t.Errorf("cooldown count = %d, want 1", got)
	}

	now = now.Add(2 * time.Minute)
	if got := client.cool.len(); got != 0 {
		t.Errorf("cooldown count after expiry = %d, want 0", got)
	}
	if _, err := client.NewRequest("orders", http.MethodGet, "/orders").Do(t.Context()); err != nil {
		t.Fatalf("Do() after cooldown error = %v", err)
	}
	hosts, _ := transport.snapshot()
	if !reflect.DeepEqual(hosts, []string{"a", "b", "b", "a"}) {
		t.Errorf("transport hosts = %v, want [a b b a]", hosts)
	}
}

func TestFailover_AllCooldownStillAttempts(t *testing.T) {
	instance := Instance{ID: "a", Host: "a", Port: 8080}
	transport := &fakeTransport{next: make(map[string]int)}
	client := newFailoverTestClient(
		fakeDiscovery{instances: []Instance{instance}},
		transport,
		WithCooldown(time.Minute),
	)
	client.cool.mark(instance)

	response, err := client.NewRequest("orders", http.MethodGet, "/orders").Do(t.Context())
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if response == nil || response.StatusCode != http.StatusOK {
		t.Errorf("Do() response = %#v, want status %d", response, http.StatusOK)
	}
	hosts, _ := transport.snapshot()
	if !reflect.DeepEqual(hosts, []string{"a"}) {
		t.Errorf("transport hosts = %v, want [a]", hosts)
	}
}

func TestDo_DiscoveryErrors(t *testing.T) {
	emptyClient := New(fakeDiscovery{})
	_, err := emptyClient.NewRequest("orders", http.MethodGet, "/orders").Do(t.Context())
	if !errors.Is(err, ErrNoInstances) {
		t.Errorf("empty discovery error = %v, want %v", err, ErrNoInstances)
	}

	discoveryErr := errors.New("registry offline")
	client := New(fakeDiscovery{err: discoveryErr})
	_, err = client.NewRequest("orders", http.MethodGet, "/orders").Do(t.Context())
	if err == nil || errors.Is(err, ErrNoInstances) || !errors.Is(err, discoveryErr) {
		t.Errorf("discovery error = %v, want wrapped registry error", err)
	}
	if err == nil || !strings.Contains(err.Error(), "discover") {
		t.Errorf("discovery error = %v, want wrapped discover context", err)
	}
}

func TestDo_TransportErrorsExhausted(t *testing.T) {
	discovery := fakeDiscovery{instances: []Instance{
		{ID: "a", Host: "a", Port: 8080},
		{ID: "b", Host: "b", Port: 8080},
	}}
	transport := &fakeTransport{results: map[string][]fakeTransportResult{
		"a": {{err: errors.New("connection failed")}},
		"b": {{err: errors.New("connection failed")}},
	}, next: make(map[string]int)}
	client := newFailoverTestClient(discovery, transport, WithMaxAttempts(2))

	response, err := client.NewRequest("orders", http.MethodGet, "/orders").Do(t.Context())
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("Do() error = %v, want *TransportError", err)
	}
	if response != nil {
		t.Errorf("Do() response = %#v, want nil for transport failures", response)
	}
	if !strings.Contains(err.Error(), "after 2 attempts") {
		t.Errorf("Do() error = %q, want attempt count", err)
	}
}

func TestDo_TransportErrorAttempts(t *testing.T) {
	transport := &fakeTransport{results: map[string][]fakeTransportResult{
		"a": {{err: &TransportError{Err: errors.New("connection failed")}}},
	}, next: make(map[string]int)}
	client := newFailoverTestClient(
		fakeDiscovery{instances: []Instance{{ID: "a", Host: "a", Port: 8080}}},
		transport,
		WithMaxAttempts(1),
	)

	_, err := client.NewRequest("orders", http.MethodGet, "/orders").Do(t.Context())
	if got, want := err.Error(), "jinx: transport: after 1 attempts: connection failed"; got != want {
		t.Errorf("Do() error = %q, want %q", got, want)
	}
}

func TestDo_MarshalErrorNoNetwork(t *testing.T) {
	transport := &fakeTransport{next: make(map[string]int)}
	client := newFailoverTestClient(
		fakeDiscovery{instances: []Instance{{ID: "a", Host: "a", Port: 8080}}},
		transport,
	)

	_, err := client.NewRequest("orders", http.MethodPost, "/orders").
		SetBody(make(chan int)).
		Do(t.Context())
	if err == nil || !strings.Contains(err.Error(), "marshal") {
		t.Errorf("Do() error = %v, want marshal error", err)
	}
	hosts, _ := transport.snapshot()
	if len(hosts) != 0 {
		t.Errorf("transport calls = %d, want 0", len(hosts))
	}
}

func TestDo_ResultDecodeError(t *testing.T) {
	transport := &fakeTransport{results: map[string][]fakeTransportResult{
		"a": {{statusCode: http.StatusOK, body: "not-json"}},
	}, next: make(map[string]int)}
	client := newFailoverTestClient(
		fakeDiscovery{instances: []Instance{{ID: "a", Host: "a", Port: 8080}}},
		transport,
	)
	var result struct {
		Name string `json:"name"`
	}

	response, err := client.NewRequest("orders", http.MethodGet, "/orders").
		SetResult(&result).
		Do(t.Context())
	if err == nil || !strings.Contains(err.Error(), "decode result") {
		t.Errorf("Do() error = %v, want result decode error", err)
	}
	if response == nil || response.StatusCode != http.StatusOK {
		t.Errorf("Do() response = %#v, want 200 response", response)
	}
}

func TestDo_NoRepeatSameInstance(t *testing.T) {
	instance := Instance{ID: "a", Host: "a", Port: 8080}
	transport := &fakeTransport{results: map[string][]fakeTransportResult{
		"a": {{statusCode: http.StatusInternalServerError, body: "still down"}},
	}, next: make(map[string]int)}
	client := newFailoverTestClient(fakeDiscovery{instances: []Instance{instance}}, transport)

	response, err := client.NewRequest("orders", http.MethodGet, "/orders").Do(t.Context())
	if err == nil {
		t.Fatal("Do() error = nil, want 5xx status error")
	}
	if response == nil || response.StatusCode != http.StatusInternalServerError {
		t.Errorf("Do() response = %#v, want status %d", response, http.StatusInternalServerError)
	}
	hosts, _ := transport.snapshot()
	if len(hosts) != 1 {
		t.Errorf("transport calls = %d, want 1", len(hosts))
	}
}

func TestClient_PrefixJoin(t *testing.T) {
	instance := Instance{ID: "a", Host: "a", Port: 8080}
	transport := &fakeTransport{next: make(map[string]int)}
	client := newFailoverTestClient(fakeDiscovery{instances: []Instance{instance}}, transport)

	_, err := client.NewRequest("orders", http.MethodGet, joinPath("/v1", "/orders/:id")).
		SetPathParam("id", "42").
		Do(t.Context())
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	_, urls := transport.snapshot()
	if len(urls) != 1 || urls[0] != "http://a:8080/v1/orders/42" {
		t.Errorf("transport URLs = %v, want [http://a:8080/v1/orders/42]", urls)
	}
}

func TestDo_MissingPathParamNoNetwork(t *testing.T) {
	transport := &fakeTransport{next: make(map[string]int)}
	client := newFailoverTestClient(
		fakeDiscovery{instances: []Instance{{ID: "a", Host: "a", Port: 8080}}},
		transport,
	)

	_, err := client.NewRequest("orders", http.MethodGet, "/orders/:id").Do(t.Context())
	if err == nil || !strings.Contains(err.Error(), `missing path param "id"`) {
		t.Errorf("Do() error = %v, want missing path param error", err)
	}
	hosts, _ := transport.snapshot()
	if len(hosts) != 0 {
		t.Errorf("transport calls = %d, want 0", len(hosts))
	}
}

func TestClient_ConcurrentRR(t *testing.T) {
	discovery := fakeDiscovery{instances: []Instance{
		{ID: "a", Host: "a", Port: 8080},
		{ID: "b", Host: "b", Port: 8080},
	}}
	transport := &fakeTransport{next: make(map[string]int)}
	client := newFailoverTestClient(discovery, transport)
	var wg sync.WaitGroup
	errs := make(chan error, 100*10)

	for range 100 {
		wg.Go(func() {
			for range 10 {
				if _, err := client.NewRequest("orders", http.MethodGet, "/orders").Do(t.Context()); err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("Do() error = %v", err)
	}

	hosts, _ := transport.snapshot()
	counts := map[string]int{}
	for _, host := range hosts {
		counts[host]++
	}
	if len(hosts) != 1000 || counts["a"] != 500 || counts["b"] != 500 {
		t.Errorf("host counts = %v across %d calls, want 500 each across 1000", counts, len(hosts))
	}
}
