package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zeromicro/go-zero/tools/goctl/api/parser"
	"github.com/zeromicro/go-zero/tools/goctl/api/spec"
)

func TestGenerate_Golden(t *testing.T) {
	api := parseFixture(t)
	api.Service.Groups = api.Service.Groups[:1]

	dir := t.TempDir()
	writeModuleFile(t, dir)
	if err := gen(api, dir); err != nil {
		t.Fatalf("gen(): %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "order_api.go"))
	if err != nil {
		t.Fatalf("ReadFile(order_api.go): %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "client.golden"))
	if err != nil {
		t.Fatalf("ReadFile(client.golden): %v", err)
	}
	if !bytes.Equal(normalizeLineEndings(got), normalizeLineEndings(want)) {
		ng, nw := normalizeLineEndings(got), normalizeLineEndings(want)
		for i := 0; i < min(len(ng), len(nw)); i++ {
			if ng[i] != nw[i] {
				t.Errorf("generated client differs from golden at byte %d: got %q want %q\n--- got ---\n%s\n--- want ---\n%s", i, ng[i:min(i+20, len(ng))], nw[i:min(i+20, len(nw))], got, want)
				return
			}
		}
		t.Errorf("generated client differs from golden at length: got %d want %d\n--- got ---\n%s\n--- want ---\n%s", len(ng), len(nw), got, want)
	}
}

func TestGenerate_MixedJSONAndFormError(t *testing.T) {
	api, err := parser.Parse(filepath.Join("testdata", "mixed.api"))
	if err != nil {
		t.Fatalf("parser.Parse(mixed.api): %v", err)
	}
	dir := t.TempDir()
	writeModuleFile(t, dir)
	err = gen(api, dir)
	if err == nil || !strings.Contains(err.Error(), `mixes json and form fields`) {
		t.Fatalf("gen() error = %v, want mixed json and form error", err)
	}
}

func TestGenerate_DeleteHasNoBody(t *testing.T) {
	api := parseFixture(t)
	api.Service.Groups = api.Service.Groups[:1]
	api.Service.Groups[0].Routes[1].Method = "delete"

	dir := t.TempDir()
	writeModuleFile(t, dir)
	if err := gen(api, dir); err != nil {
		t.Fatalf("gen(): %v", err)
	}
	generated, err := os.ReadFile(filepath.Join(dir, "order_api.go"))
	if err != nil {
		t.Fatalf("ReadFile(order_api.go): %v", err)
	}
	source := string(generated)
	start := strings.Index(source, "func (c *orderApiClient) GetOrder")
	if start < 0 {
		t.Fatal("generated DELETE method not found")
	}
	method := source[start:]
	end := strings.Index(method, "\n}\n")
	if end < 0 {
		t.Fatal("generated DELETE method is not closed")
	}
	if strings.Contains(method[:end], "SetBody") {
		t.Errorf("generated DELETE client includes a request body:\n%s", method[:end])
	}
}

func TestGenerate_MatchesDirectoryPackage(t *testing.T) {
	api := parseFixture(t)
	api.Service.Groups = api.Service.Groups[:1]
	dir := t.TempDir()
	writeModuleFile(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "existing.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(existing.go): %v", err)
	}
	if err := gen(api, dir); err != nil {
		t.Fatalf("gen(): %v", err)
	}
	generated, err := os.ReadFile(filepath.Join(dir, "order_api.go"))
	if err != nil {
		t.Fatalf("ReadFile(order_api.go): %v", err)
	}
	if !bytes.HasPrefix(generated, []byte("package app\n")) {
		t.Errorf("generated package = %q, want app", strings.SplitN(string(generated), "\n", 2)[0])
	}
}

func TestDeriveTypesPkg(t *testing.T) {
	t.Run("reads module path", func(t *testing.T) {
		dir := t.TempDir()
		writeModuleFile(t, dir)
		got, err := deriveTypesPkg(dir)
		if err != nil {
			t.Fatalf("deriveTypesPkg(): %v", err)
		}
		if got != "example.com/svc/types" {
			t.Errorf("deriveTypesPkg() = %q, want example.com/svc/types", got)
		}
	})

	t.Run("accepts module comments", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/svc // module path\n\ngo 1.25\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(go.mod): %v", err)
		}
		got, err := deriveTypesPkg(dir)
		if err != nil {
			t.Fatalf("deriveTypesPkg(): %v", err)
		}
		if got != "example.com/svc/types" {
			t.Errorf("deriveTypesPkg() = %q, want example.com/svc/types", got)
		}
	})

	t.Run("walks up to module root", func(t *testing.T) {
		root := t.TempDir()
		writeModuleFile(t, root)
		dir := filepath.Join(root, "api", "orders")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(): %v", err)
		}
		got, err := deriveTypesPkg(dir)
		if err != nil {
			t.Fatalf("deriveTypesPkg(): %v", err)
		}
		if got != "example.com/svc/api/orders/types" {
			t.Errorf("deriveTypesPkg() = %q, want example.com/svc/api/orders/types", got)
		}
	})

	t.Run("reports missing go.mod", func(t *testing.T) {
		_, err := deriveTypesPkg(t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "jinx-goctl: cannot derive types package from") {
			t.Fatalf("deriveTypesPkg() error = %v, want cannot derive types package error", err)
		}
	})
}

func TestGeneratedClientBuilds(t *testing.T) {
	dir := prepareGeneratedClientModule(t)
	commands := [][]string{{"mod", "tidy"}, {"build", "./..."}, {"vet", "./..."}}
	for _, args := range commands {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
}

func TestGeneratedClientFallbacks(t *testing.T) {
	dir := prepareGeneratedClientModule(t)
	if err := os.WriteFile(filepath.Join(dir, "client_test.go"), []byte(generatedFallbackTests), 0o600); err != nil {
		t.Fatalf("WriteFile(client_test.go): %v", err)
	}

	for _, args := range [][]string{{"mod", "tidy"}, {"test", "./...", "-count=1"}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
}

func prepareGeneratedClientModule(t *testing.T) string {
	t.Helper()
	api := parseFixture(t)
	api.Service.Groups = api.Service.Groups[:1]
	dir := t.TempDir()
	writeBuildModule(t, dir)
	if err := gen(api, dir); err != nil {
		t.Fatalf("gen(): %v", err)
	}
	typesSource, err := os.ReadFile(filepath.Join(dir, "types", "order_api.go"))
	if err != nil {
		t.Fatalf("ReadFile(types.go): %v", err)
	}
	for _, want := range []string{"GetOrderReq", "GetOrderResp", "CreateOrderReq", "CreateOrderResp", "SearchOrderReq", "UpdateStatusReq"} {
		if !strings.Contains(string(typesSource), want) {
			t.Errorf("generated types.go missing %q:\n%s", want, typesSource)
		}
	}
	return dir
}

const generatedFallbackTests = `package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	types "example.com/svc/types"
	"github.com/anyoptional/jinx"
)

type fallbackAPI struct {
	calls int
	err   error
}

func (f *fallbackAPI) CreateOrder(context.Context, *types.CreateOrderReq) (*types.CreateOrderResp, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &types.CreateOrderResp{Id: 99}, nil
}

func (f *fallbackAPI) GetOrder(_ context.Context, req *types.GetOrderReq) (*types.GetOrderResp, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &types.GetOrderResp{Id: req.Id + 1000}, nil
}

func (f *fallbackAPI) DeleteOrder(_ context.Context, _ *types.GetOrderReq) error {
	f.calls++
	return f.err
}

func (f *fallbackAPI) ListOrders(_ context.Context, _ *types.SearchOrderReq) (*types.GetOrderResp, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &types.GetOrderResp{Id: 2000}, nil
}

func (f *fallbackAPI) UpdateOrderStatus(_ context.Context, _ *types.UpdateStatusReq) (*types.GetOrderResp, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &types.GetOrderResp{Id: 3000}, nil
}

type fakeDiscovery struct {
	instances []jinx.Instance
	err       error
}

func (d fakeDiscovery) Description() string { return "fixture" }
func (d fakeDiscovery) Instances(context.Context, string) ([]jinx.Instance, error) {
	return d.instances, d.err
}
func (fakeDiscovery) Services(context.Context) ([]string, error) { return nil, nil }

func response(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func newClient(
	t *testing.T,
	target jinx.Target,
	discovery jinx.DiscoveryClient,
	transport jinx.Transport,
	fallback *fallbackAPI,
	availability func(*jinx.Failure) bool,
	options ...jinx.Option,
) OrderApi {
	t.Helper()
	clientOptions := []jinx.Option{jinx.WithMaxAttempts(1), jinx.WithTransport(transport)}
	clientOptions = append(clientOptions, options...)
	client, err := NewOrderApiClient(OrderApiClientConfig{
		Target:       target,
		Discovery:    discovery,
		ClientOptions: clientOptions,
		Fallback:     fallback,
		Availability: availability,
	})
	if err != nil {
		t.Fatalf("NewOrderApiClient(): %v", err)
	}
	return client
}

func TestStaticFallbackRunsForAvailabilityFailures(t *testing.T) {
	target := jinx.Target{Name: "orders"}
	direct := jinx.Target{Name: "orders", URL: "http://orders.test"}
	neverTransport := func(context.Context, *http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected transport call")
	}
	transportFailure := func(context.Context, *http.Request) (*http.Response, error) {
		return nil, errors.New("connection failed")
	}
	timeout := func(ctx context.Context, _ *http.Request) (*http.Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	statusFailure := func(_ context.Context, req *http.Request) (*http.Response, error) {
		return response(req, http.StatusServiceUnavailable, "down"), nil
	}
	tests := []struct {
		name      string
		target    jinx.Target
		discovery jinx.DiscoveryClient
		transport jinx.Transport
		options   []jinx.Option
	}{
		{name: "discovery failure", target: target, discovery: fakeDiscovery{err: errors.New("registry offline")}, transport: neverTransport},
		{name: "empty pool", target: target, discovery: fakeDiscovery{}, transport: neverTransport},
		{name: "transport failure", target: direct, transport: transportFailure},
		{name: "attempt timeout", target: direct, transport: timeout, options: []jinx.Option{jinx.WithTimeout(5 * time.Millisecond)}},
		{name: "final 5xx", target: direct, transport: statusFailure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fallback := &fallbackAPI{}
			client := newClient(t, tt.target, tt.discovery, tt.transport, fallback, nil, tt.options...)
			result, err := client.GetOrder(t.Context(), &types.GetOrderReq{Id: 42})
			if err != nil {
				t.Fatalf("GetOrder(): %v", err)
			}
			if result == nil || result.Id != 1042 {
				t.Errorf("fallback result = %#v, want order id 1042", result)
			}
			if fallback.calls != 1 {
				t.Errorf("fallback calls = %d, want 1", fallback.calls)
			}
		})
	}
}

func TestDefaultAvailabilitySkipsClientAndDecodeFailures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		wantKind  jinx.Kind
	}{
		{name: "429", status: http.StatusTooManyRequests, body: "busy", wantKind: jinx.KindStatus},
		{name: "decode", status: http.StatusOK, body: "not-json", wantKind: jinx.KindDecode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fallback := &fallbackAPI{}
			client := newClient(t, jinx.Target{Name: "orders", URL: "http://orders.test"}, nil, func(_ context.Context, req *http.Request) (*http.Response, error) {
				return response(req, tt.status, tt.body), nil
			}, fallback, nil)
			_, err := client.GetOrder(t.Context(), &types.GetOrderReq{Id: 42})
			var failure *jinx.Failure
			if !errors.As(err, &failure) || failure.Kind != tt.wantKind {
				t.Fatalf("GetOrder() error = %v, want %v", err, tt.wantKind)
			}
			if fallback.calls != 0 {
				t.Errorf("fallback calls = %d, want 0", fallback.calls)
			}
		})
	}
}

func TestCallerCancellationSkipsFallback(t *testing.T) {
	fallback := &fallbackAPI{}
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	client := newClient(t, jinx.Target{Name: "orders", URL: "http://orders.test"}, nil, func(context.Context, *http.Request) (*http.Response, error) {
		calls++
		cancel()
		return nil, context.Canceled
	}, fallback, nil)

	_, err := client.GetOrder(ctx, &types.GetOrderReq{Id: 42})
	var failure *jinx.Failure
	if !errors.As(err, &failure) || failure.Kind != jinx.KindCanceled {
		t.Fatalf("GetOrder() error = %v, want canceled failure", err)
	}
	if calls != 1 || fallback.calls != 0 {
		t.Errorf("transport calls = %d, fallback calls = %d", calls, fallback.calls)
	}
}

func TestCustomAvailabilityCanRecover429AndDecode(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "429", status: http.StatusTooManyRequests, body: "busy"},
		{name: "decode", status: http.StatusOK, body: "not-json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fallback := &fallbackAPI{}
			client := newClient(t, jinx.Target{Name: "orders", URL: "http://orders.test"}, nil, func(_ context.Context, req *http.Request) (*http.Response, error) {
				return response(req, tt.status, tt.body), nil
			}, fallback, func(*jinx.Failure) bool { return true })
			result, err := client.GetOrder(t.Context(), &types.GetOrderReq{Id: 42})
			if err != nil {
				t.Fatalf("GetOrder(): %v", err)
			}
			if result == nil || result.Id != 1042 || fallback.calls != 1 {
				t.Errorf("result = %#v, fallback calls = %d", result, fallback.calls)
			}
		})
	}
}

func TestFallbackFactoryReceivesFailureAndResponseAndJoinsErrors(t *testing.T) {
	fallbackErr := errors.New("fallback failed")
	fallback := &fallbackAPI{err: fallbackErr}
	var factoryFailure *jinx.Failure
	var factoryResponse *jinx.Response
	client, err := NewOrderApiClient(OrderApiClientConfig{
		Target: jinx.Target{Name: "orders", URL: "http://orders.test"},
		ClientOptions: []jinx.Option{
			jinx.WithMaxAttempts(1),
			jinx.WithTransport(func(_ context.Context, req *http.Request) (*http.Response, error) {
				return response(req, http.StatusBadGateway, "unavailable"), nil
			}),
		},
		FallbackFactory: func(_ context.Context, failure *jinx.Failure, response *jinx.Response) (OrderApi, error) {
			factoryFailure = failure
			factoryResponse = response
			return fallback, nil
		},
	})
	if err != nil {
		t.Fatalf("NewOrderApiClient(): %v", err)
	}

	_, err = client.GetOrder(t.Context(), &types.GetOrderReq{Id: 42})
	if !errors.Is(err, fallbackErr) {
		t.Errorf("GetOrder() error = %v, want fallback error", err)
	}
	var gotFailure *jinx.Failure
	if !errors.As(err, &gotFailure) || gotFailure != factoryFailure || gotFailure.Kind != jinx.KindStatus {
		t.Errorf("joined failure = %#v, factory failure = %#v", gotFailure, factoryFailure)
	}
	if factoryResponse == nil || factoryResponse.StatusCode != http.StatusBadGateway {
		t.Errorf("factory response = %#v, want 502 response", factoryResponse)
	}
}

func TestFallbackFactoryReceivesNilResponseForTransportFailure(t *testing.T) {
	var factoryResponse *jinx.Response
	fallback := &fallbackAPI{}
	client, err := NewOrderApiClient(OrderApiClientConfig{
		Target: jinx.Target{Name: "orders", URL: "http://orders.test"},
		ClientOptions: []jinx.Option{
			jinx.WithMaxAttempts(1),
			jinx.WithTransport(func(context.Context, *http.Request) (*http.Response, error) {
				return nil, errors.New("connection failed")
			}),
		},
		FallbackFactory: func(_ context.Context, _ *jinx.Failure, response *jinx.Response) (OrderApi, error) {
			factoryResponse = response
			return fallback, nil
		},
	})
	if err != nil {
		t.Fatalf("NewOrderApiClient(): %v", err)
	}
	if _, err := client.GetOrder(t.Context(), &types.GetOrderReq{Id: 42}); err != nil {
		t.Fatalf("GetOrder(): %v", err)
	}
	if factoryResponse != nil {
		t.Errorf("factory response = %#v, want nil for transport failure", factoryResponse)
	}
}

func TestStaticFallbackAndFactoryAreMutuallyExclusive(t *testing.T) {
	fallback := &fallbackAPI{}
	_, err := NewOrderApiClient(OrderApiClientConfig{
		Target:   jinx.Target{Name: "orders", URL: "http://orders.test"},
		Fallback: fallback,
		FallbackFactory: func(context.Context, *jinx.Failure, *jinx.Response) (OrderApi, error) {
			return fallback, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("NewOrderApiClient() error = %v, want mutually exclusive fallback error", err)
	}
}

func TestErrorOnlyRouteFallback(t *testing.T) {
	fallback := &fallbackAPI{}
	client := newClient(t, jinx.Target{Name: "orders", URL: "http://orders.test"}, nil, func(_ context.Context, req *http.Request) (*http.Response, error) {
		return response(req, http.StatusBadGateway, "unavailable"), nil
	}, fallback, nil)
	if err := client.DeleteOrder(t.Context(), &types.GetOrderReq{Id: 7}); err != nil {
		t.Fatalf("DeleteOrder(): %v", err)
	}
	if fallback.calls != 1 {
		t.Errorf("fallback calls = %d, want 1", fallback.calls)
	}

	fallbackErr := errors.New("delete fallback failed")
	fallback = &fallbackAPI{err: fallbackErr}
	client = newClient(t, jinx.Target{Name: "orders", URL: "http://orders.test"}, nil, func(_ context.Context, req *http.Request) (*http.Response, error) {
		return response(req, http.StatusBadGateway, "unavailable"), nil
	}, fallback, nil)
	err := client.DeleteOrder(t.Context(), &types.GetOrderReq{Id: 7})
	if !errors.Is(err, fallbackErr) {
		t.Errorf("DeleteOrder() error = %v, want fallback error", err)
	}
	var failure *jinx.Failure
	if !errors.As(err, &failure) || failure.Kind != jinx.KindStatus {
		t.Errorf("joined failure = %#v, want KindStatus", failure)
	}
}

func TestTagWiring(t *testing.T) {
	fallback := &fallbackAPI{}
	var gotPath, gotHeader, gotQuery, gotFormBody, gotContentType string
	capture := func(_ context.Context, req *http.Request) (*http.Response, error) {
		gotPath = req.URL.Path
		gotHeader = req.Header.Get("token")
		gotQuery = req.URL.RawQuery
		gotContentType = req.Header.Get("Content-Type")
		if req.Body != nil {
			body, _ := io.ReadAll(req.Body)
			gotFormBody = string(body)
		}
		return response(req, http.StatusOK, "{\"id\":42}"), nil
	}
	client := newClient(t, jinx.Target{Name: "orders", URL: "http://orders.test"}, nil, capture, fallback, nil)

	if _, err := client.GetOrder(t.Context(), &types.GetOrderReq{Id: 42, Token: "t-1"}); err != nil {
		t.Fatalf("GetOrder(): %v", err)
	}
	if gotPath != "/api/v1/orders/42" {
		t.Errorf("path = %q, want /api/v1/orders/42", gotPath)
	}
	if gotHeader != "t-1" {
		t.Errorf("header = %q, want t-1", gotHeader)
	}

	if _, err := client.ListOrders(t.Context(), &types.SearchOrderReq{Status: "open"}); err != nil {
		t.Fatalf("ListOrders(): %v", err)
	}
	if gotQuery != "status=open" {
		t.Errorf("query = %q, want status=open", gotQuery)
	}

	if _, err := client.UpdateOrderStatus(t.Context(), &types.UpdateStatusReq{Id: 7, Status: "done"}); err != nil {
		t.Fatalf("UpdateOrderStatus(): %v", err)
	}
	if gotPath != "/api/v1/orders/7/status" {
		t.Errorf("path = %q, want /api/v1/orders/7/status", gotPath)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Errorf("content type = %q, want application/x-www-form-urlencoded", gotContentType)
	}
	if gotFormBody != "status=done" {
		t.Errorf("form body = %q, want status=done", gotFormBody)
	}
}
`

func parseFixture(t *testing.T) *spec.ApiSpec {
	t.Helper()
	api, err := parser.Parse(filepath.Join("testdata", "order.api"))
	if err != nil {
		t.Fatalf("parser.Parse(): %v", err)
	}
	return api
}

func writeModuleFile(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/svc\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod): %v", err)
	}
}

func writeBuildModule(t *testing.T, dir string) {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	coreDir, err := filepath.Abs(filepath.Join(filepath.Dir(sourceFile), ".."))
	if err != nil {
		t.Fatalf("filepath.Abs(): %v", err)
	}
	content := fmt.Sprintf(
		"module example.com/svc\n\ngo 1.25\n\nrequire github.com/anyoptional/jinx v0.0.0\n\nreplace github.com/anyoptional/jinx => %s\n",
		filepath.ToSlash(coreDir),
	)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod): %v", err)
	}
	sum, err := os.ReadFile(filepath.Join(coreDir, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(core go.sum): %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0o600); err != nil {
		t.Fatalf("WriteFile(go.sum): %v", err)
	}
}

func normalizeLineEndings(content []byte) []byte {
	return bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
}
