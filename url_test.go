package jinx

import (
	"strings"
	"testing"
)

func TestBuildURL(t *testing.T) {
	tests := []struct {
		name       string
		instance   Instance
		path       string
		pathParams map[string]string
		want       string
		wantErr    string
	}{
		{
			name:     "plain HTTP",
			instance: Instance{Host: "h", Port: 8080},
			path:     "/a",
			want:     "http://h:8080/a",
		},
		{
			name:     "secure HTTP",
			instance: Instance{Host: "h", Port: 443, Secure: true},
			path:     "/a",
			want:     "https://h:443/a",
		},
		{
			name:       "path parameter",
			instance:   Instance{Host: "h", Port: 8080},
			path:       "/orders/:id",
			pathParams: map[string]string{"id": "42"},
			want:       "http://h:8080/orders/42",
		},
		{
			name:       "missing path parameter",
			instance:   Instance{Host: "h", Port: 8080},
			path:       "/orders/:id",
			pathParams: map[string]string{},
			wantErr:    `missing path param "id"`,
		},
		{
			name:       "prefix joins without duplicate slash",
			instance:   Instance{Host: "h", Port: 8080},
			path:       joinPath("/v1/", "/orders/:id"),
			pathParams: map[string]string{"id": "42"},
			want:       "http://h:8080/v1/orders/42",
		},
		{
			name:     "empty path",
			instance: Instance{Host: "h", Port: 8080},
			want:     "http://h:8080/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildURL(tt.instance, tt.path, tt.pathParams)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("buildURL() error = %v, want text containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildURL() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("buildURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
