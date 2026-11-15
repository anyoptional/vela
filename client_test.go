package vela

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/go-logr/logr"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	client, err := New(Target{Name: "orders"}, WithDiscovery(fakeDiscovery{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

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
	if client.cfg.logger.GetSink() == nil {
		t.Error("default logger has no sink")
	}
	if client.cfg.httpClient == nil {
		t.Error("httpClient is nil")
	}
}

func TestClient_NewRequest(t *testing.T) {
	client, err := New(Target{Name: "orders"}, WithDiscovery(fakeDiscovery{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if request := client.NewRequest(http.MethodGet, "/orders"); request == nil {
		t.Fatal("NewRequest() returned nil")
	}
}

func TestNew_Options(t *testing.T) {
	lb := testLoadBalancer{}
	logger := logr.Discard()
	httpClient := &http.Client{Timeout: time.Second}
	timeout := 250 * time.Millisecond
	cooldown := 2 * time.Second

	client, err := New(Target{Name: "orders"}, WithDiscovery(fakeDiscovery{}),
		WithLoadBalancer(lb),
		WithTimeout(timeout),
		WithMaxAttempts(7),
		WithCooldown(cooldown),
		WithLogger(logger),
		WithHTTPClient(httpClient),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

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
	if client.cfg.logger.Enabled() {
		t.Error("logger was not applied: want disabled discard logger")
	}
	if client.cfg.httpClient != httpClient {
		t.Error("httpClient was not applied")
	}
}

func TestNew_InvalidOptionsFallback(t *testing.T) {
	client, err := New(Target{Name: "orders"}, WithDiscovery(fakeDiscovery{}),
		WithTimeout(-time.Second),
		WithMaxAttempts(0),
		WithCooldown(-time.Second),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if got, want := client.cfg.perAttemptTimeout, 5*time.Second; got != want {
		t.Errorf("perAttemptTimeout = %v, want %v", got, want)
	}
	if got, want := client.cfg.maxAttempts, 3; got != want {
		t.Errorf("maxAttempts = %d, want %d", got, want)
	}
	if got := client.cfg.cooldown; got != 0 {
		t.Errorf("cooldown = %v, want 0", got)
	}

	zeroCooldown, err := New(Target{Name: "orders"}, WithDiscovery(fakeDiscovery{}), WithCooldown(0))
	if err != nil {
		t.Fatalf("New() with zero cooldown error = %v", err)
	}
	if got, want := zeroCooldown.cfg.cooldown, 10*time.Second; got != want {
		t.Errorf("WithCooldown(0) cooldown = %v, want %v", got, want)
	}
}

type fakeDiscovery struct {
	instances []Instance
	err       error
	calls     *int
}

func (d fakeDiscovery) Description() string {
	return "fake"
}

func (d fakeDiscovery) Instances(context.Context, string) ([]Instance, error) {
	if d.calls != nil {
		(*d.calls)++
	}
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
	mu           sync.Mutex
	results      map[string][]fakeTransportResult
	next         map[string]int
	hosts        []string
	urls         []string
	bodies       []string
	contentTypes []string
}

func (f *fakeTransport) do(_ context.Context, req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	host := req.URL.Hostname()
	fullURL := req.URL.String()
	// Mirror resty's LoadBalancer composition: service-mode requests carry
	// a relative path and the chosen base URL in the context.
	if selection, ok := req.Context().Value(attemptSelectionKey{}).(*attemptSelection); ok && selection != nil {
		if base, err := url.Parse(selection.base); err == nil {
			host = base.Hostname()
		}
		fullURL = selection.base + req.URL.String()
	}
	f.hosts = append(f.hosts, host)
	f.urls = append(f.urls, fullURL)
	var body string
	if req.Body != nil {
		read, err := io.ReadAll(req.Body)
		if err == nil {
			body = string(read)
		}
	}
	f.bodies = append(f.bodies, body)
	f.contentTypes = append(f.contentTypes, req.Header.Get("Content-Type"))
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

func (f *fakeTransport) requestSnapshot() (bodies, contentTypes []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies...), append([]string(nil), f.contentTypes...)
}

func newFailoverTestClient(t *testing.T, discovery DiscoveryClient, transport *fakeTransport, opts ...Option) *Client {
	t.Helper()
	options := append([]Option{WithDiscovery(discovery)}, opts...)
	client, err := New(Target{Name: "orders"}, options...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
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
	client := newFailoverTestClient(t, discovery, transport)
	var result struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	response, err := client.NewRequest(http.MethodGet, "/orders/42").
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
	client := newFailoverTestClient(t, discovery, transport)

	response, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
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
	client := newFailoverTestClient(t, discovery, transport)

	response, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
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
	client := newFailoverTestClient(t, discovery, transport,
		WithLoadBalancer(firstLoadBalancer{}),
		WithCooldown(time.Minute),
	)
	client.cool.now = func() time.Time { return now }

	for range 2 {
		if _, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context()); err != nil {
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
	if _, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context()); err != nil {
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
	client := newFailoverTestClient(t,
		fakeDiscovery{instances: []Instance{instance}},
		transport,
		WithCooldown(time.Minute),
	)
	client.cool.mark(instance)

	response, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
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
	emptyClient := newFailoverTestClient(t, fakeDiscovery{}, &fakeTransport{next: make(map[string]int)})
	_, err := emptyClient.NewRequest(http.MethodGet, "/orders").Do(t.Context())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != KindDiscovery || !errors.Is(failure, ErrNoInstances) {
		t.Errorf("empty discovery error = %v, want KindDiscovery wrapping %v", err, ErrNoInstances)
	}

	discoveryErr := errors.New("registry offline")
	client := newFailoverTestClient(t, fakeDiscovery{err: discoveryErr}, &fakeTransport{next: make(map[string]int)})
	_, err = client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
	if !errors.As(err, &failure) || failure.Kind != KindDiscovery || errors.Is(err, ErrNoInstances) || !errors.Is(err, discoveryErr) {
		t.Errorf("discovery error = %v, want wrapped registry error", err)
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
	client := newFailoverTestClient(t, discovery, transport, WithMaxAttempts(2))

	response, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != KindTransport {
		t.Fatalf("Do() error = %v, want KindTransport Failure", err)
	}
	if response != nil {
		t.Errorf("Do() response = %#v, want nil for transport failures", response)
	}
	if failure.attempt != 2 || failure.attemptLimit != 2 {
		t.Errorf("Failure attempt = %d/%d, want 2/2", failure.attempt, failure.attemptLimit)
	}
}

func TestDo_TransportErrorAttempts(t *testing.T) {
	cause := errors.New("connection failed")
	transport := &fakeTransport{results: map[string][]fakeTransportResult{
		"a": {{err: cause}},
	}, next: make(map[string]int)}
	client := newFailoverTestClient(t,
		fakeDiscovery{instances: []Instance{{ID: "a", Host: "a", Port: 8080}}},
		transport,
		WithMaxAttempts(1),
	)

	_, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != KindTransport {
		t.Fatalf("Do() error = %v, want KindTransport Failure", err)
	}
	if !errors.Is(failure, cause) {
		t.Errorf("Failure does not unwrap transport cause %v", cause)
	}
}

func TestDo_MarshalErrorNoNetwork(t *testing.T) {
	transport := &fakeTransport{next: make(map[string]int)}
	client := newFailoverTestClient(t,
		fakeDiscovery{instances: []Instance{{ID: "a", Host: "a", Port: 8080}}},
		transport,
	)

	_, err := client.NewRequest(http.MethodPost, "/orders").
		SetBody(make(chan int)).
		Do(t.Context())
	var failure *Failure
	var marshalErr *json.UnsupportedTypeError
	if !errors.As(err, &failure) || failure.Kind != KindInvalidCall || !errors.As(failure, &marshalErr) {
		t.Errorf("Do() error = %v, want KindInvalidCall wrapping marshal error", err)
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
	client := newFailoverTestClient(t,
		fakeDiscovery{instances: []Instance{{ID: "a", Host: "a", Port: 8080}}},
		transport,
	)
	var result struct {
		Name string `json:"name"`
	}

	response, err := client.NewRequest(http.MethodGet, "/orders").
		SetResult(&result).
		Do(t.Context())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != KindDecode {
		t.Errorf("Do() error = %v, want KindDecode Failure", err)
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
	client := newFailoverTestClient(t, fakeDiscovery{instances: []Instance{instance}}, transport)

	response, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
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
	client := newFailoverTestClient(t, fakeDiscovery{instances: []Instance{instance}}, transport)

	_, err := client.NewRequest(http.MethodGet, joinPath("/v1", "/orders/:id")).
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
	discoveryCalls := 0
	client := newFailoverTestClient(t,
		fakeDiscovery{
			instances: []Instance{{ID: "a", Host: "a", Port: 8080}},
			calls:     &discoveryCalls,
		},
		transport,
	)

	_, err := client.NewRequest(http.MethodGet, "/orders/:id").Do(t.Context())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != KindInvalidCall ||
		!strings.Contains(failure.Err.Error(), `missing path param "id"`) {
		t.Errorf("Do() error = %v, want missing path param error", err)
	}
	hosts, _ := transport.snapshot()
	if len(hosts) != 0 {
		t.Errorf("transport calls = %d, want 0", len(hosts))
	}
	if discoveryCalls != 0 {
		t.Errorf("discovery calls = %d, want 0", discoveryCalls)
	}
}

func TestClient_ConcurrentRR(t *testing.T) {
	discovery := fakeDiscovery{instances: []Instance{
		{ID: "a", Host: "a", Port: 8080},
		{ID: "b", Host: "b", Port: 8080},
	}}
	transport := &fakeTransport{next: make(map[string]int)}
	client := newFailoverTestClient(t, discovery, transport)
	var wg sync.WaitGroup
	errs := make(chan error, 100*10)

	for range 100 {
		wg.Go(func() {
			for range 10 {
				if _, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context()); err != nil {
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

func TestDo_TransportErrorAfterStatusClearsResponse(t *testing.T) {
	discovery := fakeDiscovery{instances: []Instance{
		{ID: "a", Host: "a", Port: 8080},
		{ID: "b", Host: "b", Port: 8080},
	}}
	transport := &fakeTransport{results: map[string][]fakeTransportResult{
		"a": {{statusCode: http.StatusInternalServerError, body: "boom"}},
		"b": {{err: errors.New("connection refused")}},
	}, next: make(map[string]int)}
	client := newFailoverTestClient(t, discovery, transport, WithLoadBalancer(firstLoadBalancer{}))

	resp, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != KindTransport {
		t.Fatalf("Do() error = %v, want KindTransport", err)
	}
	if resp != nil {
		t.Errorf("Do() response = %+v, want nil after transport failure on final attempt", resp)
	}
}

func TestDo_CancelReturnsLastResponse(t *testing.T) {
	discovery := fakeDiscovery{instances: []Instance{{ID: "a", Host: "a", Port: 8080}}}
	transport := &fakeTransport{results: map[string][]fakeTransportResult{
		"a": {{statusCode: http.StatusInternalServerError, body: "boom"}},
	}, next: make(map[string]int)}
	var cancel context.CancelFunc
	client := newFailoverTestClient(t, discovery, transport,
		WithAttemptPolicies([]AttemptPolicy{
			func(ctx context.Context, _ *Failure, _ AttemptState) (AttemptAction, error) {
				cancel()
				return AttemptRetrySameEndpoint, nil
			},
		}))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	resp, err := client.NewRequest(http.MethodGet, "/orders").Do(ctx)
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != KindCanceled {
		t.Fatalf("Do() error = %v, want KindCanceled", err)
	}
	if resp == nil || resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("Do() response = %+v, want the 500 response received before cancel", resp)
	}
}

func TestDo_ServiceModeEscapesPath(t *testing.T) {
	discovery := fakeDiscovery{instances: []Instance{{ID: "a", Host: "a", Port: 8080}}}
	transport := &fakeTransport{next: make(map[string]int)}
	client := newFailoverTestClient(t, discovery, transport)

	if _, err := client.NewRequest(http.MethodGet, "/files/a?admin=true#frag").Do(t.Context()); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	_, urls := transport.snapshot()
	want := "http://a:8080/files/a%3Fadmin=true%23frag"
	if len(urls) != 1 || urls[0] != want {
		t.Errorf("request URL = %v, want [%s]", urls, want)
	}
}

func TestDo_FormParamsEncoded(t *testing.T) {
	discovery := fakeDiscovery{instances: []Instance{{ID: "a", Host: "a", Port: 8080}}}
	transport := &fakeTransport{next: make(map[string]int)}
	client := newFailoverTestClient(t, discovery, transport)

	if _, err := client.NewRequest(http.MethodPost, "/orders").
		SetFormParam("name", "a b&c").
		SetFormParam("status", "1").
		Do(t.Context()); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	bodies, contentTypes := transport.requestSnapshot()
	if len(bodies) != 1 || bodies[0] != "name=a+b%26c&status=1" {
		t.Errorf("request bodies = %q, want [name=a+b%%26c&status=1]", bodies)
	}
	if len(contentTypes) != 1 || contentTypes[0] != "application/x-www-form-urlencoded" {
		t.Errorf("content types = %q, want [application/x-www-form-urlencoded]", contentTypes)
	}
}

func TestDo_FormAndBodyConflict(t *testing.T) {
	discovery := fakeDiscovery{instances: []Instance{{ID: "a", Host: "a", Port: 8080}}}
	transport := &fakeTransport{next: make(map[string]int)}
	client := newFailoverTestClient(t, discovery, transport)

	_, err := client.NewRequest(http.MethodPost, "/orders").
		SetFormParam("name", "x").
		SetBody(map[string]any{"name": "x"}).
		Do(t.Context())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != KindInvalidCall {
		t.Fatalf("Do() error = %v, want KindInvalidCall", err)
	}
}

func TestRequest_SetTimeoutOverridesClient(t *testing.T) {
	var gotDeadline time.Time
	client, err := New(Target{Name: "orders", URL: "http://orders.test"},
		WithTimeout(5*time.Second),
		WithTransport(func(ctx context.Context, _ *http.Request) (*http.Response, error) {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Error("attempt context has no deadline")
			}
			gotDeadline = deadline
			return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: http.NoBody}, nil
		}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	start := time.Now()
	if _, err := client.NewRequest(http.MethodGet, "/orders").SetTimeout(100 * time.Millisecond).Do(t.Context()); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if remain := time.Until(gotDeadline); remain < 50*time.Millisecond || remain > 150*time.Millisecond {
		t.Errorf("attempt deadline in %v from start, want ~100ms", gotDeadline.Sub(start))
	}
}

func TestRequest_SetTimeoutZeroInheritsClient(t *testing.T) {
	var gotDeadline time.Time
	client, err := New(Target{Name: "orders", URL: "http://orders.test"},
		WithTimeout(2*time.Second),
		WithTransport(func(ctx context.Context, _ *http.Request) (*http.Response, error) {
			deadline, _ := ctx.Deadline()
			gotDeadline = deadline
			return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: http.NoBody}, nil
		}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	start := time.Now()
	if _, err := client.NewRequest(http.MethodGet, "/orders").SetTimeout(0).Do(t.Context()); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if remain := time.Until(gotDeadline); remain < 1500*time.Millisecond || remain > 2500*time.Millisecond {
		t.Errorf("attempt deadline in %v from start, want ~2s client default", gotDeadline.Sub(start))
	}
}

func TestNew_ConnectTimeoutDefaultClient(t *testing.T) {
	client, err := New(Target{Name: "orders", URL: "http://orders.test"}, WithConnectTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	// The default client is always wrapped by otelhttp; the dial timeout
	// lives on the wrapped transport and cannot be inspected structurally.
	if _, ok := client.cfg.httpClient.Transport.(*otelhttp.Transport); !ok {
		t.Fatalf("default httpClient transport = %T, want *otelhttp.Transport", client.cfg.httpClient.Transport)
	}

	noConnect, err := New(Target{Name: "orders", URL: "http://orders.test"}, WithConnectTimeout(0))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, ok := noConnect.cfg.httpClient.Transport.(*otelhttp.Transport); !ok {
		t.Fatalf("default httpClient transport = %T, want *otelhttp.Transport", noConnect.cfg.httpClient.Transport)
	}
}

func TestWithConnectTimeout_IgnoredWithCustomHTTPClient(t *testing.T) {
	custom := &http.Client{Timeout: 3 * time.Second}
	client, err := New(Target{Name: "orders", URL: "http://orders.test"},
		WithConnectTimeout(50*time.Millisecond),
		WithHTTPClient(custom),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client.cfg.httpClient != custom {
		t.Error("custom httpClient was replaced")
	}
}

func TestDefaultClient_PropagatesTraceContext(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	const traceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	var gotTraceparent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	ctx := otel.GetTextMapPropagator().Extract(context.Background(), propagation.MapCarrier{"traceparent": traceparent})

	client, err := New(Target{Name: "orders", URL: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := client.NewRequest(http.MethodGet, "/health").Do(ctx); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if gotTraceparent != traceparent {
		t.Errorf("server received traceparent = %q, want %q", gotTraceparent, traceparent)
	}
}
