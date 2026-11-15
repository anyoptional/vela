package vela

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAttemptPolicy_NextAndFirstDecisionWins(t *testing.T) {
	var calls []string
	statuses := []int{http.StatusServiceUnavailable, http.StatusNoContent}
	transportCalls := 0
	client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test"},
		WithMaxAttempts(2),
		WithAttemptPolicies([]AttemptPolicy{
			func(context.Context, *Failure, AttemptState) (AttemptAction, error) {
				calls = append(calls, "next")
				return AttemptNext, nil
			},
			func(context.Context, *Failure, AttemptState) (AttemptAction, error) {
				calls = append(calls, "retry")
				return AttemptRetrySameEndpoint, nil
			},
			func(context.Context, *Failure, AttemptState) (AttemptAction, error) {
				calls = append(calls, "stop")
				return AttemptStop, nil
			},
		}),
		WithTransport(func(_ context.Context, req *http.Request) (*http.Response, error) {
			status := statuses[transportCalls]
			transportCalls++
			return strategyResponse(req, status, ""), nil
		}),
	)

	if _, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context()); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if got, want := strings.Join(calls, ","), "next,retry"; got != want {
		t.Errorf("policy calls = %q, want %q", got, want)
	}
	if transportCalls != 2 {
		t.Errorf("transport calls = %d, want 2", transportCalls)
	}
}

func TestAttemptPolicy_ReplacesDefaultsAndCanRetry429(t *testing.T) {
	t.Run("replacement does not inherit default 5xx retry", func(t *testing.T) {
		policyCalls := 0
		transportCalls := 0
		client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test"},
			WithMaxAttempts(3),
			WithAttemptPolicies([]AttemptPolicy{
				func(context.Context, *Failure, AttemptState) (AttemptAction, error) {
					policyCalls++
					return AttemptNext, nil
				},
			}),
			WithTransport(func(_ context.Context, req *http.Request) (*http.Response, error) {
				transportCalls++
				return strategyResponse(req, http.StatusServiceUnavailable, "down"), nil
			}),
		)

		_, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
		requireFailureKind(t, err, KindStatus)
		if policyCalls != 1 || transportCalls != 1 {
			t.Errorf("policy calls = %d, transport calls = %d, want one attempt", policyCalls, transportCalls)
		}
	})

	t.Run("custom policy retries 429", func(t *testing.T) {
		transportCalls := 0
		client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test"},
			WithMaxAttempts(2),
			WithAttemptPolicies([]AttemptPolicy{
				func(_ context.Context, failure *Failure, _ AttemptState) (AttemptAction, error) {
					if failure.Kind == KindStatus && failure.StatusCode == http.StatusTooManyRequests {
						return AttemptRetrySameEndpoint, nil
					}
					return AttemptStop, nil
				},
			}),
			WithTransport(func(_ context.Context, req *http.Request) (*http.Response, error) {
				transportCalls++
				status := http.StatusTooManyRequests
				if transportCalls == 2 {
					status = http.StatusNoContent
				}
				return strategyResponse(req, status, ""), nil
			}),
		)

		response, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		if transportCalls != 2 || response == nil || response.StatusCode != http.StatusNoContent {
			t.Errorf("transport calls = %d, response = %#v, want successful second attempt", transportCalls, response)
		}
	})
}

func TestAttemptPolicy_ErrorPreservesFailure(t *testing.T) {
	policyErr := errors.New("policy rejected failure")
	var original *Failure
	client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test"},
		WithMaxAttempts(2),
		WithAttemptPolicies([]AttemptPolicy{
			func(_ context.Context, failure *Failure, _ AttemptState) (AttemptAction, error) {
				original = failure
				return AttemptNext, policyErr
			},
		}),
		WithTransport(func(_ context.Context, req *http.Request) (*http.Response, error) {
			return strategyResponse(req, http.StatusServiceUnavailable, "down"), nil
		}),
	)

	_, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != KindInvalidCall {
		t.Fatalf("Do() error = %v, want KindInvalidCall Failure", err)
	}
	if !errors.Is(err, policyErr) {
		t.Errorf("Do() error %v does not preserve policy error", err)
	}
	var cause *Failure
	if !errors.As(failure.Err, &cause) || cause != original || cause.Kind != KindStatus {
		t.Errorf("policy failure cause = %#v, want original status failure %#v", cause, original)
	}
}

func TestDirectTarget_RetriesSameEndpointWithinBudget(t *testing.T) {
	var urls []string
	statuses := []int{
		http.StatusServiceUnavailable,
		http.StatusServiceUnavailable,
		http.StatusNoContent,
	}
	client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test/base"},
		WithMaxAttempts(len(statuses)),
		WithTransport(func(_ context.Context, req *http.Request) (*http.Response, error) {
			urls = append(urls, req.URL.String())
			return strategyResponse(req, statuses[len(urls)-1], ""), nil
		}),
	)

	if _, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context()); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if len(urls) != 3 {
		t.Fatalf("transport calls = %d, want 3", len(urls))
	}
	for _, got := range urls {
		if got != "http://orders.test/base/orders" {
			t.Errorf("request URL = %q, want same direct endpoint", got)
		}
	}
}

func TestServiceTarget_FailsOverOnlyToUntriedInstances(t *testing.T) {
	discovery := fakeDiscovery{instances: []Instance{
		{ID: "a", Host: "a", Port: 8080},
		{ID: "b", Host: "b", Port: 8080},
		{ID: "c", Host: "c", Port: 8080},
	}}
	transport := &fakeTransport{results: map[string][]fakeTransportResult{
		"a": {{err: errors.New("connection failed")}},
		"b": {{err: errors.New("connection failed")}},
		"c": {{err: errors.New("connection failed")}},
	}, next: make(map[string]int)}
	client := newFailoverTestClient(t, discovery, transport, WithMaxAttempts(3))

	_, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
	requireFailureKind(t, err, KindTransport)
	hosts, _ := transport.snapshot()
	if len(hosts) != 3 {
		t.Fatalf("transport hosts = %v, want three attempts", hosts)
	}
	seen := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		if _, ok := seen[host]; ok {
			t.Errorf("host %q was retried instead of failing over", host)
		}
		seen[host] = struct{}{}
	}
	if len(seen) != len(discovery.instances) {
		t.Errorf("attempted hosts = %v, want each untried instance once", hosts)
	}
}

func TestDo_ClassifiesBoundariesAndUnwrapsCause(t *testing.T) {
	t.Run("invalid request", func(t *testing.T) {
		client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test"},
			WithTransport(func(context.Context, *http.Request) (*http.Response, error) {
				t.Fatal("transport called for missing path parameter")
				return nil, nil
			}),
		)
		_, err := client.NewRequest(http.MethodGet, "/orders/:id").Do(t.Context())
		requireFailureKind(t, err, KindInvalidCall)
	})

	t.Run("discovery error", func(t *testing.T) {
		cause := errors.New("registry offline")
		client := newStrategyClient(t, Target{Name: "orders"}, WithDiscovery(fakeDiscovery{err: cause}))
		_, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
		failure := requireFailureKind(t, err, KindDiscovery)
		if !errors.Is(failure, cause) {
			t.Errorf("failure does not unwrap discovery cause %v", cause)
		}
	})

	t.Run("invalid load balancer result", func(t *testing.T) {
		calls := 0
		client := newStrategyClient(t, Target{Name: "orders"},
			WithDiscovery(fakeDiscovery{instances: []Instance{{ID: "a", Host: "a", Port: 8080}}}),
			WithLoadBalancer(invalidLoadBalancer{}),
			WithTransport(func(_ context.Context, req *http.Request) (*http.Response, error) {
				calls++
				return strategyResponse(req, http.StatusNoContent, ""), nil
			}),
		)
		_, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
		requireFailureKind(t, err, KindInvalidCall)
		if calls != 0 {
			t.Errorf("transport calls = %d, want 0 for invalid selection", calls)
		}
	})

	t.Run("empty discovery pool", func(t *testing.T) {
		client := newStrategyClient(t, Target{Name: "orders"}, WithDiscovery(fakeDiscovery{}))
		_, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
		failure := requireFailureKind(t, err, KindDiscovery)
		if !errors.Is(failure, ErrNoInstances) {
			t.Errorf("failure does not unwrap %v", ErrNoInstances)
		}
	})

	t.Run("transport error", func(t *testing.T) {
		cause := errors.New("connection reset")
		client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test"},
			WithMaxAttempts(1),
			WithTransport(func(context.Context, *http.Request) (*http.Response, error) {
				return nil, cause
			}),
		)
		_, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
		failure := requireFailureKind(t, err, KindTransport)
		if !errors.Is(failure, cause) {
			t.Errorf("failure does not unwrap transport cause %v", cause)
		}
	})

	t.Run("transport error preserves received response", func(t *testing.T) {
		cause := errors.New("transport reported a response error")
		client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test"},
			WithMaxAttempts(1),
			WithTransport(func(_ context.Context, req *http.Request) (*http.Response, error) {
				return strategyResponse(req, http.StatusBadGateway, "partial response"), cause
			}),
		)
		response, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
		failure := requireFailureKind(t, err, KindTransport)
		if !errors.Is(failure, cause) {
			t.Errorf("failure does not unwrap transport cause %v", cause)
		}
		if response == nil || response.StatusCode != http.StatusBadGateway || string(response.Body) != "partial response" {
			t.Errorf("response = %#v, want preserved 502 response", response)
		}
	})

	t.Run("full 4xx response", func(t *testing.T) {
		body := strings.Repeat("x", 700)
		calls := 0
		client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test"},
			WithTransport(func(_ context.Context, req *http.Request) (*http.Response, error) {
				calls++
				return strategyResponse(req, http.StatusTooManyRequests, body), nil
			}),
		)
		response, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
		failure := requireFailureKind(t, err, KindStatus)
		if failure.StatusCode != http.StatusTooManyRequests {
			t.Errorf("failure status = %d, want %d", failure.StatusCode, http.StatusTooManyRequests)
		}
		if response == nil || string(response.Body) != body {
			t.Errorf("response body length = %d, want %d", len(response.Body), len(body))
		}
		if calls != 1 {
			t.Errorf("transport calls = %d, want 1 for 4xx", calls)
		}
		if strings.Contains(err.Error(), body) {
			t.Errorf("failure error includes unbounded response body")
		}
	})

	t.Run("final 5xx response", func(t *testing.T) {
		calls := 0
		client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test"},
			WithMaxAttempts(2),
			WithTransport(func(_ context.Context, req *http.Request) (*http.Response, error) {
				calls++
				return strategyResponse(req, http.StatusBadGateway, "unavailable"), nil
			}),
		)
		response, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
		failure := requireFailureKind(t, err, KindStatus)
		if failure.StatusCode != http.StatusBadGateway || response == nil || response.StatusCode != http.StatusBadGateway {
			t.Errorf("final result = response %#v, failure %#v", response, failure)
		}
		if calls != 2 {
			t.Errorf("transport calls = %d, want 2", calls)
		}
	})

	t.Run("decode error", func(t *testing.T) {
		client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test"},
			WithTransport(func(_ context.Context, req *http.Request) (*http.Response, error) {
				return strategyResponse(req, http.StatusOK, "not-json"), nil
			}),
		)
		var result struct {
			Name string `json:"name"`
		}
		response, err := client.NewRequest(http.MethodGet, "/orders").SetResult(&result).Do(t.Context())
		failure := requireFailureKind(t, err, KindDecode)
		if response == nil || response.StatusCode != http.StatusOK || failure.StatusCode != 0 {
			t.Errorf("decode result = response %#v, failure %#v", response, failure)
		}
	})
}

func TestAttemptPolicy_CanRetryDecodeFailure(t *testing.T) {
	calls := 0
	client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test"},
		WithMaxAttempts(2),
		WithAttemptPolicies([]AttemptPolicy{
			func(_ context.Context, failure *Failure, _ AttemptState) (AttemptAction, error) {
				if failure.Kind == KindDecode {
					return AttemptRetrySameEndpoint, nil
				}
				return AttemptNext, nil
			},
		}),
		WithTransport(func(_ context.Context, req *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return strategyResponse(req, http.StatusOK, "not-json"), nil
			}
			return strategyResponse(req, http.StatusOK, `{"name":"recovered"}`), nil
		}),
	)
	var result struct {
		Name string `json:"name"`
	}

	response, err := client.NewRequest(http.MethodGet, "/orders").SetResult(&result).Do(t.Context())
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if calls != 2 {
		t.Errorf("transport calls = %d, want 2", calls)
	}
	if result.Name != "recovered" {
		t.Errorf("decoded result = %#v, want recovered name", result)
	}
	if response == nil || response.StatusCode != http.StatusOK {
		t.Errorf("response = %#v, want status %d", response, http.StatusOK)
	}
}

func TestDo_DistinguishesCallerCancellationFromAttemptTimeout(t *testing.T) {
	t.Run("caller cancellation", func(t *testing.T) {
		started := make(chan struct{})
		var once sync.Once
		calls := 0
		client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test"},
			WithMaxAttempts(3),
			WithAttemptPolicies([]AttemptPolicy{
				func(context.Context, *Failure, AttemptState) (AttemptAction, error) {
					return AttemptRetrySameEndpoint, nil
				},
			}),
			WithTransport(func(ctx context.Context, _ *http.Request) (*http.Response, error) {
				calls++
				once.Do(func() { close(started) })
				<-ctx.Done()
				return nil, ctx.Err()
			}),
		)
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan error, 1)
		go func() {
			_, err := client.NewRequest(http.MethodGet, "/orders").Do(ctx)
			result <- err
		}()
		<-started
		cancel()
		err := <-result
		requireFailureKind(t, err, KindCanceled)
		if calls != 1 {
			t.Errorf("transport calls after cancellation = %d, want 1", calls)
		}
	})

	t.Run("cancellation during response read does not cool instance", func(t *testing.T) {
		instance := Instance{ID: "a", Host: "a", Port: 8080}
		readStarted := make(chan struct{})
		client := newStrategyClient(t, Target{Name: "orders"},
			WithDiscovery(fakeDiscovery{instances: []Instance{instance}}),
			WithCooldown(time.Minute),
			WithTransport(func(ctx context.Context, req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       &blockingContextBody{ctx: ctx, started: readStarted},
					Request:    req,
				}, nil
			}),
		)
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan error, 1)
		go func() {
			_, err := client.NewRequest(http.MethodGet, "/orders").Do(ctx)
			result <- err
		}()
		<-readStarted
		cancel()
		requireFailureKind(t, <-result, KindCanceled)
		if client.cool.isCooling(instance) {
			t.Error("instance entered cooldown after caller cancellation")
		}
	})

	t.Run("per attempt timeout", func(t *testing.T) {
		server := newSlowResponseServer(t, 100*time.Millisecond)
		client := newStrategyClient(t, Target{Name: "orders", URL: server.URL},
			WithTimeout(10*time.Millisecond),
			WithMaxAttempts(1),
			WithHTTPClient(server.Client()),
		)
		_, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
		requireFailureKind(t, err, KindTransport)
	})

	t.Run("custom transport receives attempt deadline", func(t *testing.T) {
		client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test"},
			WithTimeout(10*time.Millisecond),
			WithMaxAttempts(1),
			WithTransport(func(ctx context.Context, _ *http.Request) (*http.Response, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(100 * time.Millisecond):
					return nil, errors.New("attempt deadline was not propagated")
				}
			}),
		)
		_, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
		failure := requireFailureKind(t, err, KindTransport)
		if !errors.Is(failure, context.DeadlineExceeded) {
			t.Errorf("failure = %v, want per-attempt deadline cause", failure)
		}
	})
}

func TestDo_ReplaysJSONBodyAcrossAttempts(t *testing.T) {
	statuses := []int{http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusNoContent}
	var bodies []string
	client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test"},
		WithMaxAttempts(len(statuses)),
		WithTransport(func(_ context.Context, req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			bodies = append(bodies, string(body))
			return strategyResponse(req, statuses[len(bodies)-1], ""), nil
		}),
	)

	if _, err := client.NewRequest(http.MethodPost, "/orders").
		SetBody(map[string]string{"name": "desk"}).
		Do(t.Context()); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if len(bodies) != len(statuses) {
		t.Fatalf("attempt body count = %d, want %d", len(bodies), len(statuses))
	}
	for _, body := range bodies {
		if body != `{"name":"desk"}` {
			t.Errorf("attempt body = %q, want replayed JSON body", body)
		}
	}
}

func TestDo_ConcurrentAttemptStateIsRequestLocal(t *testing.T) {
	var mu sync.Mutex
	calls := make(map[string]int)
	client := newStrategyClient(t, Target{Name: "orders", URL: "http://orders.test"},
		WithMaxAttempts(2),
		WithTransport(func(_ context.Context, req *http.Request) (*http.Response, error) {
			id := req.Header.Get("X-Request-ID")
			mu.Lock()
			calls[id]++
			count := calls[id]
			mu.Unlock()
			if count == 1 {
				return strategyResponse(req, http.StatusServiceUnavailable, ""), nil
			}
			return strategyResponse(req, http.StatusNoContent, ""), nil
		}),
	)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for id := range 2 {
		wg.Go(func() {
			_, err := client.NewRequest(http.MethodGet, "/orders").
				SetHeader("X-Request-ID", string(rune('a'+id))).
				Do(t.Context())
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Do() error = %v", err)
		}
	}
	if len(calls) != 2 || calls["a"] != 2 || calls["b"] != 2 {
		t.Errorf("per-request transport calls = %v, want a:2 and b:2", calls)
	}
}

func TestTransportOption_OverridesDefault(t *testing.T) {
	calls := 0
	client := newStrategyClient(t, Target{Name: "orders", URL: "http://127.0.0.1:1"},
		WithTransport(func(_ context.Context, req *http.Request) (*http.Response, error) {
			calls++
			return strategyResponse(req, http.StatusNoContent, ""), nil
		}),
	)

	response, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if response == nil || response.StatusCode != http.StatusNoContent || calls != 1 {
		t.Errorf("response = %#v, transport calls = %d", response, calls)
	}
}

func TestFailover_NoCooldownOn5xx(t *testing.T) {
	instance := Instance{ID: "a", Host: "a", Port: 8080}
	client := newFailoverTestClient(t, fakeDiscovery{instances: []Instance{instance}},
		&fakeTransport{results: map[string][]fakeTransportResult{
			"a": {{statusCode: http.StatusServiceUnavailable}},
		}, next: make(map[string]int)},
		WithMaxAttempts(1),
	)

	_, err := client.NewRequest(http.MethodGet, "/orders").Do(t.Context())
	requireFailureKind(t, err, KindStatus)
	if client.cool.isCooling(instance) {
		t.Error("instance entered cooldown after 5xx response")
	}
}

type invalidLoadBalancer struct{}

func (invalidLoadBalancer) Choose(context.Context, string, []Instance) (Instance, error) {
	return Instance{}, nil
}

type blockingContextBody struct {
	ctx     context.Context
	started chan struct{}
	once    sync.Once
}

func (b *blockingContextBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (*blockingContextBody) Close() error {
	return nil
}

func newStrategyClient(t *testing.T, target Target, options ...Option) *Client {
	t.Helper()
	client, err := New(target, options...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func strategyResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func requireFailureKind(t *testing.T, err error, kind Kind) *Failure {
	t.Helper()
	var failure *Failure
	if !errors.As(err, &failure) {
		t.Fatalf("error = %v, want *Failure", err)
	}
	if failure.Kind != kind {
		t.Fatalf("Failure.Kind = %v, want %v", failure.Kind, kind)
	}
	return failure
}

func newSlowResponseServer(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(delay)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return server
}
