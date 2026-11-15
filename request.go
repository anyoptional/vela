package jinx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Request builds and executes a request to a service instance.
type Request struct {
	method     string
	path       string
	pathParams map[string]string
	query      map[string]string
	headers    http.Header
	body       any
	result     any
	c          *Client
}

// SetPathParam sets the value for a path parameter.
func (r *Request) SetPathParam(name, value string) *Request {
	if r.pathParams == nil {
		r.pathParams = make(map[string]string)
	}
	r.pathParams[name] = value
	return r
}

// SetQueryParam sets a query parameter.
func (r *Request) SetQueryParam(name, value string) *Request {
	if r.query == nil {
		r.query = make(map[string]string)
	}
	r.query[name] = value
	return r
}

// SetHeader sets a request header.
func (r *Request) SetHeader(name, value string) *Request {
	if r.headers == nil {
		r.headers = make(http.Header)
	}
	r.headers.Set(name, value)
	return r
}

// SetBody sets the value to JSON-encode as the request body.
func (r *Request) SetBody(v any) *Request {
	r.body = v
	return r
}

// SetResult sets the destination for a successful response body.
func (r *Request) SetResult(v any) *Request {
	r.result = v
	return r
}

// Do executes the request.
func (r *Request) Do(ctx context.Context) (*Response, error) {
	if r == nil || r.c == nil {
		return nil, errors.New("jinx: request has no client")
	}
	direct := r.c.target.Mode() == TargetDirect
	if !direct && r.c.discovery == nil {
		return nil, errors.New("jinx: discovery client is nil")
	}

	resolvedPath, err := substitutePathParams(joinPath(r.c.target.Path, r.path), r.pathParams)
	if err != nil {
		return nil, err
	}

	var instances []Instance
	if direct {
		instances = []Instance{{ID: r.c.target.Name}}
	} else {
		instances, err = r.c.discovery.Instances(ctx, r.c.target.Name)
		if err != nil {
			return nil, fmt.Errorf("jinx: discover %q: %w", r.c.target.Name, err)
		}
		if len(instances) == 0 {
			return nil, ErrNoInstances
		}
	}

	var body []byte
	if r.body != nil {
		body, err = json.Marshal(r.body)
		if err != nil {
			return nil, fmt.Errorf("jinx: marshal request body: %w", err)
		}
	}
	if r.c.transport == nil {
		return nil, errors.New("jinx: transport is not configured")
	}

	tried := make(map[insKey]struct{}, len(instances))
	attempts := 0
	var lastResponse *Response
	var lastStatusErr error
	var lastTransportErr error

	for attempts < r.c.cfg.maxAttempts {
		candidates := make([]Instance, 0, len(instances))
		available := make([]Instance, 0, len(instances))
		for _, instance := range instances {
			key := keyFor(instance)
			if _, ok := tried[key]; ok {
				continue
			}
			candidates = append(candidates, instance)
			if direct || !r.c.cool.isCooling(instance) {
				available = append(available, instance)
			}
		}
		if len(available) > 0 {
			candidates = available
		}
		if len(candidates) == 0 {
			break
		}

		instance := candidates[0]
		var targetURL string
		if direct {
			targetURL, err = buildDirectURL(r.c.target.URL, "", resolvedPath)
			if err != nil {
				return nil, fmt.Errorf("jinx: build direct URL: %w", err)
			}
		} else {
			instance, err = r.c.lb.Choose(ctx, r.c.target.Name, candidates)
			if err != nil {
				return nil, fmt.Errorf("jinx: choose instance: %w", err)
			}
			targetURL = buildURLFromPath(instance, resolvedPath)
		}

		var bodyReader io.Reader
		if r.body != nil {
			bodyReader = bytes.NewReader(body)
		}
		httpRequest, err := http.NewRequestWithContext(ctx, r.method, targetURL, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("jinx: build request: %w", err)
		}
		if len(r.query) > 0 {
			query := httpRequest.URL.Query()
			for key, value := range r.query {
				query.Set(key, value)
			}
			httpRequest.URL.RawQuery = query.Encode()
		}
		httpRequest.Header = r.headers.Clone()

		attempts++
		httpResponse, err := r.c.transport(ctx, httpRequest)
		tried[keyFor(instance)] = struct{}{}
		if err != nil {
			if !direct {
				r.c.cool.mark(instance)
			}
			lastTransportErr = err
			lastStatusErr = nil
			continue
		}
		if httpResponse == nil {
			lastTransportErr = errors.New("jinx: transport returned nil response")
			lastStatusErr = nil
			continue
		}

		responseBody, readErr := readResponseBody(httpResponse)
		if readErr != nil {
			if !direct {
				r.c.cool.mark(instance)
			}
			lastTransportErr = fmt.Errorf("read response body: %w", readErr)
			lastStatusErr = nil
			continue
		}
		lastTransportErr = nil
		response := &Response{
			StatusCode: httpResponse.StatusCode,
			Body:       responseBody,
		}
		lastResponse = response
		if httpResponse.StatusCode >= http.StatusOK && httpResponse.StatusCode < http.StatusMultipleChoices {
			if r.result != nil {
				if err := json.Unmarshal(responseBody, r.result); err != nil {
					return response, fmt.Errorf("jinx: decode result: %w", err)
				}
			}
			return response, nil
		}

		excerpt := responseBody[:min(len(responseBody), 512)]
		lastStatusErr = fmt.Errorf("jinx: unexpected status %d: %s", httpResponse.StatusCode, excerpt)
		if httpResponse.StatusCode >= http.StatusInternalServerError && httpResponse.StatusCode <= 599 {
			continue
		}
		return response, lastStatusErr
	}

	if lastTransportErr != nil {
		cause := lastTransportErr
		for {
			var transportErr *TransportError
			if !errors.As(cause, &transportErr) || transportErr.Err == nil {
				break
			}
			cause = transportErr.Err
		}
		return nil, &TransportError{
			Err: fmt.Errorf("after %d attempts: %w", attempts, cause),
		}
	}
	if lastResponse != nil {
		return lastResponse, lastStatusErr
	}
	return nil, ErrNoInstances
}

func readResponseBody(response *http.Response) ([]byte, error) {
	if response.Body == nil {
		return nil, nil
	}
	defer response.Body.Close()
	return io.ReadAll(response.Body)
}

// Response contains the status and body returned by an HTTP request.
type Response struct {
	StatusCode int
	Body       []byte
}
