package vela

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Request builds and executes a request to the configured target.
type Request struct {
	method     string
	path       string
	pathParams map[string]string
	query      map[string]string
	headers    http.Header
	body       any
	form       map[string]string
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

// SetFormParam sets a form field encoded as an
// application/x-www-form-urlencoded request body. It is mutually
// exclusive with SetBody: setting both fails at Do.
func (r *Request) SetFormParam(name, value string) *Request {
	if r.form == nil {
		r.form = make(map[string]string)
	}
	r.form[name] = value
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
		return nil, &Failure{Kind: KindInvalidCall, Err: errors.New("request has no client")}
	}
	if ctx == nil {
		return nil, r.c.newFailure(KindInvalidCall, errors.New("request context is nil"))
	}
	if ctx.Err() != nil {
		return nil, r.c.newFailure(KindCanceled, context.Cause(ctx))
	}

	client := r.c
	direct := client.target.Mode() == TargetDirect
	resolvedPath, err := resolveServicePath(r.path, r.pathParams)
	if err != nil {
		return nil, client.newFailure(KindInvalidCall, err)
	}

	var body []byte
	var formEncoded bool
	if r.body != nil && len(r.form) > 0 {
		return nil, client.newFailure(KindInvalidCall, errors.New("request body and form params are mutually exclusive"))
	}
	if r.body != nil {
		body, err = json.Marshal(r.body)
		if err != nil {
			return nil, client.newFailure(KindInvalidCall, fmt.Errorf("marshal request body: %w", err))
		}
	} else if len(r.form) > 0 {
		values := make(url.Values, len(r.form))
		for name, value := range r.form {
			values.Set(name, value)
		}
		body = []byte(values.Encode())
		formEncoded = true
	}
	if client.transport == nil {
		return nil, client.newFailure(KindInvalidCall, errors.New("transport is not configured"))
	}
	if !direct && client.discovery == nil {
		return nil, client.newFailure(KindInvalidCall, errors.New("discovery client is nil"))
	}
	if ctx.Err() != nil {
		return nil, client.canceledFailure(context.Cause(ctx), 0, nil, "")
	}

	var instances []Instance
	if direct {
		instances = []Instance{{ID: r.c.target.Name}}
	} else {
		instances, err = client.discovery.Instances(ctx, client.target.Name)
		if err != nil {
			if ctx.Err() != nil {
				return nil, client.canceledFailure(errors.Join(err, context.Cause(ctx)), 0, nil, "")
			}
			return nil, client.newFailure(KindDiscovery, fmt.Errorf("discover %q: %w", client.target.Name, err))
		}
		if len(instances) == 0 {
			return nil, client.newFailure(KindDiscovery, ErrNoInstances)
		}
	}
	if ctx.Err() != nil {
		return nil, client.canceledFailure(context.Cause(ctx), 0, nil, "")
	}

	tried := make(map[insKey]struct{}, len(instances))
	attempts := 0
	retrySame := false
	var currentInstance Instance
	var currentEndpoint string
	var lastResponse *Response

	var lastFailure *Failure
	for attempts < client.cfg.maxAttempts {
		if ctx.Err() != nil {
			return lastResponse, client.canceledFailure(context.Cause(ctx), attempts, lastFailure, "")
		}

		var instance Instance
		var targetURL string
		if direct {
			instance = Instance{ID: client.target.Name}
			if retrySame {
				targetURL = currentEndpoint
			} else {
				targetURL, err = buildDirectURL(client.target.URL, resolvedPath)
				if err != nil {
					return nil, client.newFailure(KindInvalidCall, fmt.Errorf("build direct URL: %w", err))
				}
			}
			currentInstance = instance
			currentEndpoint = targetURL
		} else if retrySame {
			instance = currentInstance
			targetURL = currentEndpoint
		} else {
			candidates := untriedInstances(instances, tried)
			if len(candidates) == 0 {
				break
			}
			available := make([]Instance, 0, len(candidates))
			for _, candidate := range candidates {
				if !client.cool.isCooling(candidate) {
					available = append(available, candidate)
				}
			}
			if len(available) > 0 {
				candidates = available
			}
			instance, err = client.lb.Choose(ctx, client.target.Name, candidates)
			if err != nil {
				if ctx.Err() != nil {
					return lastResponse, client.canceledFailure(errors.Join(err, context.Cause(ctx)), attempts, lastFailure, "")
				}
				return lastResponse, client.newFailure(KindInvalidCall, fmt.Errorf("choose instance: %w", err))
			}
			if !containsInstance(candidates, instance) {
				return lastResponse, client.newFailure(KindInvalidCall, errors.New("load balancer returned an unknown instance"))
			}
			targetURL = buildURLFromPath(instance, resolvedPath)
			currentInstance = instance
			currentEndpoint = targetURL
		}

		var bodyReader io.Reader
		if body != nil {
			bodyReader = bytes.NewReader(body)
		}
		attemptCtx, cancelAttempt := context.WithTimeout(ctx, client.cfg.perAttemptTimeout)
		httpRequest, err := http.NewRequestWithContext(attemptCtx, r.method, targetURL, bodyReader)
		if err != nil {
			cancelAttempt()
			return lastResponse, client.newFailure(KindInvalidCall, fmt.Errorf("build request: %w", err))
		}
		if len(r.query) > 0 {
			query := httpRequest.URL.Query()
			for key, value := range r.query {
				query.Set(key, value)
			}
			httpRequest.URL.RawQuery = query.Encode()
		}
		headers := r.headers.Clone()
		if formEncoded {
			if headers == nil {
				headers = make(http.Header)
			}
			headers.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		httpRequest.Header = headers
		if ctx.Err() != nil {
			cancelAttempt()
			return lastResponse, client.canceledFailure(context.Cause(ctx), attempts, lastFailure, targetURL)
		}

		attempts++
		httpResponse, err := client.transport(attemptCtx, httpRequest)
		attemptTimedOut := attemptCtx.Err() != nil && ctx.Err() == nil
		var failure *Failure
		if err != nil {
			if httpResponse != nil {
				responseBody, readErr := readResponseBody(httpResponse)
				lastResponse = &Response{
					StatusCode: httpResponse.StatusCode,
					Body:       responseBody,
				}
				if readErr != nil {
					err = errors.Join(err, fmt.Errorf("read response body: %w", readErr))
				}
				attemptTimedOut = attemptCtx.Err() != nil && ctx.Err() == nil
			} else {
				// No response on the final attempt: drop any response from
				// earlier attempts so the returned Response matches the failure.
				lastResponse = nil
			}
			cancelAttempt()
			if ctx.Err() != nil {
				return lastResponse, client.canceledFailure(errors.Join(err, context.Cause(ctx)), attempts, nil, targetURL)
			}
			if attemptTimedOut {
				err = errors.Join(err, context.DeadlineExceeded)
			}
			failure = client.failureForAttempt(KindTransport, 0, err, attempts, instance, targetURL, "")
		} else if httpResponse == nil {
			cancelAttempt()
			lastResponse = nil
			if ctx.Err() != nil {
				return lastResponse, client.canceledFailure(context.Cause(ctx), attempts, nil, targetURL)
			}
			var cause error = errors.New("transport returned nil response")
			if attemptTimedOut {
				cause = errors.Join(cause, context.DeadlineExceeded)
			}
			failure = client.failureForAttempt(KindTransport, 0, cause, attempts, instance, targetURL, "")
		} else {
			responseBody, readErr := readResponseBody(httpResponse)
			attemptTimedOut = attemptCtx.Err() != nil && ctx.Err() == nil
			cancelAttempt()
			lastResponse = &Response{
				StatusCode: httpResponse.StatusCode,
				Body:       responseBody,
			}
			if readErr != nil {
				if ctx.Err() != nil {
					previous := client.failureForAttempt(
						KindTransport,
						0,
						fmt.Errorf("read response body: %w", readErr),
						attempts,
						instance,
						targetURL,
						"",
					)
					return lastResponse, client.canceledFailure(context.Cause(ctx), attempts, previous, targetURL)
				}
				if attemptTimedOut {
					readErr = errors.Join(readErr, context.DeadlineExceeded)
				}
				failure = client.failureForAttempt(
					KindTransport,
					0,
					fmt.Errorf("read response body: %w", readErr),
					attempts,
					instance,
					targetURL,
					"",
				)
			} else if attemptTimedOut {
				failure = client.failureForAttempt(
					KindTransport,
					0,
					context.DeadlineExceeded,
					attempts,
					instance,
					targetURL,
					"",
				)
			} else if httpResponse.StatusCode >= http.StatusOK && httpResponse.StatusCode < http.StatusMultipleChoices {
				if r.result != nil {
					if err := json.Unmarshal(responseBody, r.result); err != nil {
						failure = client.failureForAttempt(
							KindDecode,
							0,
							fmt.Errorf("decode result: %w", err),
							attempts,
							instance,
							targetURL,
							"",
						)
					}
				}
				if failure == nil {
					return lastResponse, nil
				}
			} else {
				failure = client.failureForAttempt(
					KindStatus,
					httpResponse.StatusCode,
					fmt.Errorf("unexpected status %d", httpResponse.StatusCode),
					attempts,
					instance,
					targetURL,
					string(responseBody[:min(len(responseBody), failureExcerptLimit)]),
				)
			}
		}

		if failure.Kind == KindTransport && !direct {
			client.cool.mark(instance)
		}
		tried[keyFor(instance)] = struct{}{}
		lastFailure = failure
		if ctx.Err() != nil {
			return lastResponse, client.canceledFailure(context.Cause(ctx), attempts, failure, targetURL)
		}

		untried := 1
		if !direct {
			untried = len(untriedInstances(instances, tried))
		}
		state := AttemptState{
			Attempt:          attempts,
			AttemptLimit:     client.cfg.maxAttempts,
			UntriedEndpoints: untried,
			TargetMode:       client.target.Mode(),
			Replayable:       true,
		}
		action, policyFailure := decideAttempt(ctx, client.cfg.attemptPolicies, failure, state)
		if ctx.Err() != nil {
			previous := failure
			if policyFailure != nil {
				previous = policyFailure
			}
			return lastResponse, client.canceledFailure(context.Cause(ctx), attempts, previous, targetURL)
		}
		if policyFailure != nil {
			return lastResponse, policyFailure
		}
		if attempts >= client.cfg.maxAttempts || !state.Replayable {
			break
		}
		switch action {
		case AttemptRetrySameEndpoint:
			retrySame = true
		case AttemptFailover:
			if direct || untried == 0 {
				return lastResponse, lastFailure
			}
			retrySame = false
		default:
			return lastResponse, lastFailure
		}
	}
	return lastResponse, lastFailure
}

func (c *Client) newFailure(kind Kind, cause error) *Failure {
	mode := "service"
	if c.target.Mode() == TargetDirect {
		mode = "direct"
	}
	return &Failure{
		Kind:       kind,
		Err:        cause,
		clientName: c.target.Name,
		targetMode: mode,
	}
}

func (c *Client) failureForAttempt(kind Kind, statusCode int, cause error, attempt int, instance Instance, endpoint, excerpt string) *Failure {
	failure := c.newFailure(kind, cause)
	failure.StatusCode = statusCode
	failure.attempt = attempt
	failure.attemptLimit = c.cfg.maxAttempts
	failure.instanceID = instance.ID
	failure.endpoint = endpoint
	failure.bodyExcerpt = excerpt
	return failure
}

func (c *Client) canceledFailure(cause error, attempt int, previous *Failure, endpoint string) *Failure {
	if previous != nil {
		cause = errors.Join(previous, cause)
	}
	failure := c.newFailure(KindCanceled, cause)
	failure.attempt = attempt
	failure.attemptLimit = c.cfg.maxAttempts
	if previous != nil {
		failure.instanceID = previous.instanceID
		if endpoint == "" {
			endpoint = previous.endpoint
		}
	}
	failure.endpoint = endpoint
	return failure
}

func untriedInstances(instances []Instance, tried map[insKey]struct{}) []Instance {
	candidates := make([]Instance, 0, len(instances))
	seen := make(map[insKey]struct{}, len(instances))
	for _, instance := range instances {
		key := keyFor(instance)
		if _, ok := tried[key]; ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		candidates = append(candidates, instance)
	}
	return candidates
}

func containsInstance(instances []Instance, selected Instance) bool {
	for _, instance := range instances {
		if instance.ID == selected.ID && keyFor(instance) == keyFor(selected) {
			return true
		}
	}
	return false
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
