package jinx

import (
	"context"
	"errors"
	"net/http"
)

// Request builds and executes a request to a service instance.
type Request struct {
	serviceID  string
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
func (r *Request) Do(context.Context) (*Response, error) {
	return nil, errors.New("jinx: request execution is not implemented")
}

// Response contains the status and body returned by an HTTP request.
type Response struct {
	StatusCode int
	Body       []byte
}
