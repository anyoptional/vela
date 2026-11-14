package jinx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

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

func newRestyTransport(cfg config, logger *slog.Logger) transport {
	httpClient := cfg.httpClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	client := resty.NewWithClient(httpClient).
		SetTimeout(cfg.perAttemptTimeout).
		SetRetryCount(0).
		SetHeader("User-Agent", "jinx").
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
	return &TransportError{Err: errors.New(err.Error())}
}
