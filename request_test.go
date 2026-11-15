package jinx

import (
	"net/http"
	"reflect"
	"testing"
)

func TestRequest_Setters(t *testing.T) {
	client, err := New(Target{Name: "orders"}, WithDiscovery(fakeDiscovery{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := client.NewRequest(http.MethodPost, "/orders/:id")
	body := map[string]string{"name": "desk"}
	result := new(struct{})

	setters := []struct {
		name string
		got  *Request
	}{
		{name: "path param", got: request.SetPathParam("id", "42")},
		{name: "query param", got: request.SetQueryParam("expand", "items")},
		{name: "header", got: request.SetHeader("X-Trace", "trace-id")},
		{name: "body", got: request.SetBody(body)},
		{name: "result", got: request.SetResult(result)},
	}
	for _, tt := range setters {
		if tt.got != request {
			t.Errorf("%s setter returned %p, want original request %p", tt.name, tt.got, request)
		}
	}

	if got := request.pathParams["id"]; got != "42" {
		t.Errorf("path param = %q, want %q", got, "42")
	}
	if got := request.query["expand"]; got != "items" {
		t.Errorf("query param = %q, want %q", got, "items")
	}
	if got := request.headers.Get("X-Trace"); got != "trace-id" {
		t.Errorf("header = %q, want %q", got, "trace-id")
	}
	if !reflect.DeepEqual(request.body, body) {
		t.Errorf("body = %#v, want %#v", request.body, body)
	}
	if request.result != result {
		t.Errorf("result = %#v, want %#v", request.result, result)
	}
	if request.method != http.MethodPost || request.path != "/orders/:id" || request.c != client {
		t.Errorf("request identity fields = %#v", request)
	}
}
