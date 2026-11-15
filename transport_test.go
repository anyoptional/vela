package vela

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestRestyTransport_LoggerBridge(t *testing.T) {
	var output bytes.Buffer
	logger := logr.FromSlogHandler(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	bridge := newRestyLogrLogger(logger)

	bridge.Errorf("failure %d", 1)
	bridge.Warnf("slow %s", "request")
	bridge.Debugf("details %t", true)

	for _, want := range []string{"level=ERROR", "failure 1", "DEBUG+3", "slow request", "level=DEBUG", "details true"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("log output %q does not contain %q", output.String(), want)
		}
	}
}

func TestRestyTransport_RoundTrip(t *testing.T) {
	type observedRequest struct {
		method    string
		path      string
		query     string
		header    string
		userAgent string
		body      string
	}
	observed := make(chan observedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll(request body): %v", err)
		}
		observed <- observedRequest{
			method:    r.Method,
			path:      r.URL.Path,
			query:     r.URL.Query().Get("page"),
			header:    r.Header.Get("X-Trace"),
			userAgent: r.UserAgent(),
			body:      string(body),
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "accepted")
	}))
	defer server.Close()

	transport := newRestyTransport(config{
		perAttemptTimeout: time.Second,
		httpClient:        server.Client(),
	}, logr.Discard())
	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		server.URL+"/orders?page=3",
		strings.NewReader(`{"name":"desk"}`),
	)
	if err != nil {
		t.Fatalf("NewRequestWithContext(): %v", err)
	}
	req.Header.Set("X-Trace", "trace-id")

	response, err := transport(t.Context(), req)
	if err != nil {
		t.Fatalf("transport() error = %v", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll(response body): %v", err)
	}
	got := <-observed
	if got.method != http.MethodPost || got.path != "/orders" || got.query != "3" {
		t.Errorf("request route = %s %s? page=%s", got.method, got.path, got.query)
	}
	if got.header != "trace-id" || got.userAgent != "vela" {
		t.Errorf("request headers = trace %q, user-agent %q", got.header, got.userAgent)
	}
	if got.body != `{"name":"desk"}` {
		t.Errorf("request body = %q", got.body)
	}
	if response.StatusCode != http.StatusCreated || string(responseBody) != "accepted" {
		t.Errorf("response = status %d, body %q", response.StatusCode, responseBody)
	}
}

func TestRestyTransport_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()
	transport := newRestyTransport(config{
		perAttemptTimeout: 10 * time.Millisecond,
		httpClient:        server.Client(),
	}, logr.Discard())
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext(): %v", err)
	}

	_, err = transport(ctx, req)
	if err == nil {
		t.Fatal("transport() error = nil, want timeout error")
	}
}

func TestRestyTransport_ErrorSanitized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close()
	transport := newRestyTransport(config{
		perAttemptTimeout: time.Second,
		httpClient:        &http.Client{},
	}, logr.Discard())
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext(): %v", err)
	}
	req.URL.User = url.UserPassword("user", "password")
	req.URL.RawQuery = "token=secret"

	_, err = transport(t.Context(), req)
	if err == nil {
		t.Fatal("transport() error = nil, want connection error")
	}
	for _, secret := range []string{"user", "password", "token=secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("transport error %q exposes %q", err, secret)
		}
	}
	for current := err; current != nil; current = errors.Unwrap(current) {
		typ := reflect.TypeOf(current)
		for typ != nil && typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ != nil && strings.HasPrefix(typ.PkgPath(), "resty.dev/v3") {
			t.Errorf("error chain exposes resty type %s", typ)
		}
	}
}
