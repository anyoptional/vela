package vela

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/go-logr/logr"
	"resty.dev/v3"
)

// restyLogrLogger adapts the module-wide logr logger to resty's Logger
// interface. resty only offers printf-style levels, so mapping is
// Errorf → error, Warnf → V(1), Debugf → V(4).
type restyLogrLogger struct {
	logger logr.Logger
}

func newRestyLogrLogger(logger logr.Logger) *restyLogrLogger {
	return &restyLogrLogger{logger: logger}
}

func (l *restyLogrLogger) Errorf(format string, args ...any) {
	l.logger.Error(nil, fmt.Sprintf(format, args...))
}

func (l *restyLogrLogger) Warnf(format string, args ...any) {
	l.logger.V(1).Info(fmt.Sprintf(format, args...))
}

func (l *restyLogrLogger) Debugf(format string, args ...any) {
	l.logger.V(4).Info(fmt.Sprintf(format, args...))
}

func newRestyTransport(cfg config, logger logr.Logger) Transport {
	httpClient := cfg.httpClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	client := resty.NewWithClient(httpClient).
		SetRetryCount(0).
		SetHeader("User-Agent", "vela").
		SetLogger(newRestyLogrLogger(logger))

	return func(ctx context.Context, req *http.Request) (*http.Response, error) {
		var body []byte
		if req.Body != nil {
			var err error
			body, err = io.ReadAll(req.Body)
			closeErr := req.Body.Close()
			if err != nil {
				return nil, sanitizeTransportError(err)
			}
			if closeErr != nil {
				return nil, sanitizeTransportError(closeErr)
			}
		}

		queryParams := make(map[string]string)
		for key, values := range req.URL.Query() {
			if len(values) > 0 {
				queryParams[key] = values[len(values)-1]
			}
		}
		targetURL := *req.URL
		targetURL.RawQuery = ""
		targetURL.ForceQuery = false

		restyRequest := client.R().
			SetContext(ctx).
			SetHeaders(singleValueHeaders(req.Header)).
			SetQueryParams(queryParams)
		if req.Body != nil {
			restyRequest.SetBody(body)
		}
		response, err := restyRequest.Execute(req.Method, targetURL.String())
		if err != nil {
			return nil, sanitizeTransportError(err)
		}
		if response == nil {
			return nil, sanitizeTransportError(errors.New("resty returned nil response"))
		}

		responseBody := bytes.Clone(response.Bytes())
		return &http.Response{
			Status:        response.Status(),
			StatusCode:    response.StatusCode(),
			Header:        response.Header().Clone(),
			Body:          io.NopCloser(bytes.NewReader(responseBody)),
			ContentLength: int64(len(responseBody)),
			Request:       req,
		}, nil
	}
}

func singleValueHeaders(headers http.Header) map[string]string {
	values := make(map[string]string, len(headers))
	for key, entries := range headers {
		if len(entries) > 0 {
			values[key] = entries[len(entries)-1]
		}
	}
	return values
}

func sanitizeTransportError(err error) error {
	if err == nil {
		return nil
	}
	if urlErr, ok := err.(*url.Error); ok {
		return &url.Error{
			Op:  urlErr.Op,
			URL: sanitizeEndpoint(urlErr.URL),
			Err: sanitizeTransportError(urlErr.Err),
		}
	}
	if multiErr, ok := err.(interface{ Unwrap() []error }); ok {
		causes := multiErr.Unwrap()
		sanitized := make([]error, 0, len(causes))
		for _, cause := range causes {
			sanitized = append(sanitized, sanitizeTransportError(cause))
		}
		return errors.Join(sanitized...)
	}
	if cause := errors.Unwrap(err); cause != nil {
		return sanitizeTransportError(cause)
	}
	errType := reflect.TypeOf(err)
	for errType != nil && errType.Kind() == reflect.Pointer {
		errType = errType.Elem()
	}
	if errType != nil && strings.HasPrefix(errType.PkgPath(), "resty.dev/v3") {
		return errors.New("vela: HTTP transport failed")
	}
	return err
}
