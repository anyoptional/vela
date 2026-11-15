package vela

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"resty.dev/v3"
)

type restySlogLogger struct {
	logger *slog.Logger
}

func newRestySlogLogger(logger *slog.Logger) *restySlogLogger {
	if logger == nil {
		logger = slog.Default()
	}
	return &restySlogLogger{logger: logger}
}

func (l *restySlogLogger) Errorf(format string, args ...any) {
	l.logger.Error(fmt.Sprintf(format, args...))
}

func (l *restySlogLogger) Warnf(format string, args ...any) {
	l.logger.Warn(fmt.Sprintf(format, args...))
}

func (l *restySlogLogger) Debugf(format string, args ...any) {
	l.logger.Debug(fmt.Sprintf(format, args...))
}

func newRestyTransport(cfg config, logger *slog.Logger) Transport {
	httpClient := cfg.httpClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	client := resty.NewWithClient(httpClient).
		SetTimeout(cfg.perAttemptTimeout).
		SetRetryCount(0).
		SetHeader("User-Agent", "vela").
		SetLogger(newRestySlogLogger(logger))

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
