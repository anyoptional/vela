package jinx

import (
	"strings"
	"testing"
)

func TestResolveServicePath(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		pathParams map[string]string
		want       string
		wantErr    string
	}{
		{name: "plain", path: "/a", want: "/a"},
		{name: "empty path", path: "", want: "/"},
		{
			name:       "path parameter",
			path:       "/orders/:id",
			pathParams: map[string]string{"id": "42"},
			want:       "/orders/42",
		},
		{
			name:       "missing path parameter",
			path:       "/orders/:id",
			pathParams: map[string]string{},
			wantErr:    `missing path param "id"`,
		},
		{
			name:       "param value keeps escaping",
			path:       "/files/:name",
			pathParams: map[string]string{"name": "a/b c"},
			want:       "/files/a%2Fb%20c",
		},
		{name: "static segment with space", path: "/a b", want: "/a%20b"},
		{name: "static segment with question mark", path: "/files/a?admin=true", want: "/files/a%3Fadmin=true"},
		{name: "static segment with fragment", path: "/a#frag", want: "/a%23frag"},
		{name: "static segment with percent", path: "/a%25b", want: "/a%25b"},
		{name: "non-ASCII segment", path: "/订单", want: "/%E8%AE%A2%E5%8D%95"},
		{
			name:       "prefix joins without duplicate slash",
			path:       joinPath("/v1/", "/orders/:id"),
			pathParams: map[string]string{"id": "42"},
			want:       "/v1/orders/42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveServicePath(tt.path, tt.pathParams)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveServicePath() error = %v, want text containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveServicePath() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("resolveServicePath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildURLFromPath(t *testing.T) {
	tests := []struct {
		name     string
		instance Instance
		path     string
		want     string
	}{
		{
			name:     "plain HTTP",
			instance: Instance{Host: "h", Port: 8080},
			path:     "/a",
			want:     "http://h:8080/a",
		},
		{
			name:     "secure HTTPS",
			instance: Instance{Host: "h", Port: 443, Secure: true},
			path:     "/a",
			want:     "https://h:443/a",
		},
		{
			name:     "path without leading slash",
			instance: Instance{Host: "h", Port: 8080},
			path:     "a",
			want:     "http://h:8080/a",
		},
		{
			name:     "escaped path kept verbatim",
			instance: Instance{Host: "h", Port: 8080},
			path:     "/files/a%3Fadmin=true",
			want:     "http://h:8080/files/a%3Fadmin=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildURLFromPath(tt.instance, tt.path); got != tt.want {
				t.Errorf("buildURLFromPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildDirectURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		path    string
		want    string
	}{
		{name: "plain", baseURL: "http://h:8080", path: "/a", want: "http://h:8080/a"},
		{name: "base path joins", baseURL: "http://h:8080/base", path: "/a", want: "http://h:8080/base/a"},
		{name: "space escaped", baseURL: "http://h:8080", path: "/a b", want: "http://h:8080/a%20b"},
		{name: "query marker escaped", baseURL: "http://h:8080", path: "/files/a?admin=true", want: "http://h:8080/files/a%3Fadmin=true"},
		{name: "fragment escaped", baseURL: "http://h:8080", path: "/a#frag", want: "http://h:8080/a%23frag"},
		{name: "percent escaped", baseURL: "http://h:8080", path: "/a%25b", want: "http://h:8080/a%25b"},
		{name: "non-ASCII escaped", baseURL: "http://h:8080", path: "/订单", want: "http://h:8080/%E8%AE%A2%E5%8D%95"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildDirectURL(tt.baseURL, tt.path)
			if err != nil {
				t.Fatalf("buildDirectURL() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("buildDirectURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
