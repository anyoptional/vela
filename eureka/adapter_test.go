package eurekaadapter

import (
	"bytes"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	eureka "github.com/anyoptional/eureka-go"
	"github.com/anyoptional/vela"
)

func TestMapInstance(t *testing.T) {
	tests := []struct {
		name      string
		input     *eureka.Instance
		serviceID string
		want      vela.Instance
		wantOK    bool
	}{
		{
			name: "UP instance prefers IP and maps metadata",
			input: &eureka.Instance{
				InstanceId: "orders-1",
				App:        "ORDERS",
				Status:     eureka.UP,
				IPAddr:     "10.0.0.1",
				HostName:   "orders.local",
				Port:       eureka.Port{Number: 8080, Enabled: true},
				Metadata:   eureka.InstanceMetadata{Raw: []byte(`{"zone":"west","weight":3}`)},
			},
			serviceID: "orders",
			want: vela.Instance{
				ID:        "orders-1",
				ServiceID: "orders",
				Host:      "10.0.0.1",
				Port:      8080,
				Metadata:  map[string]string{"zone": "west", "weight": "3"},
			},
			wantOK: true,
		},
		{
			name: "falls back to hostname",
			input: &eureka.Instance{
				InstanceId: "orders-2",
				HostName:   "orders.local",
				Port:       eureka.Port{Number: 8081, Enabled: true},
			},
			serviceID: "orders",
			want: vela.Instance{
				ID:        "orders-2",
				ServiceID: "orders",
				Host:      "orders.local",
				Port:      8081,
			},
			wantOK: true,
		},
		{
			name: "secure port is preferred when enabled",
			input: &eureka.Instance{
				InstanceId: "orders-3",
				IPAddr:     "10.0.0.3",
				Port:       eureka.Port{Number: 8082, Enabled: true},
				SecurePort: eureka.Port{Number: 8443, Enabled: true},
			},
			serviceID: "orders",
			want: vela.Instance{
				ID:        "orders-3",
				ServiceID: "orders",
				Host:      "10.0.0.3",
				Port:      8443,
				Secure:    true,
			},
			wantOK: true,
		},
		{
			name: "filters instances with both ports disabled",
			input: &eureka.Instance{
				InstanceId: "orders-4",
				IPAddr:     "10.0.0.4",
				Port:       eureka.Port{Number: 8084},
				SecurePort: eureka.Port{Number: 8444},
			},
			serviceID: "orders",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.input.Metadata.Raw) > 0 {
				app := &eureka.Application{Instances: []*eureka.Instance{tt.input}}
				if err := app.ParseAllMetadata(); err != nil {
					t.Fatalf("ParseAllMetadata(): %v", err)
				}
			}
			got, ok := mapInstance(tt.input, tt.serviceID)
			if ok != tt.wantOK {
				t.Fatalf("mapInstance() ok = %v, want %v", ok, tt.wantOK)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mapInstance() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSlogLogr_Smoke(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))

	newSlogLogr(logger).Info("eureka started", "service", "orders")

	if got := output.String(); !strings.Contains(got, "eureka started") || !strings.Contains(got, "service=orders") {
		t.Errorf("log output = %q, want message and key/value", got)
	}
}
