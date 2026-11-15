package jinx

import "testing"

func TestDefaultAvailability_DefaultKindsAndStatus(t *testing.T) {
	tests := []struct {
		name    string
		failure *Failure
		want    bool
	}{
		{name: "nil failure"},
		{name: "discovery", failure: &Failure{Kind: KindDiscovery}, want: true},
		{name: "transport", failure: &Failure{Kind: KindTransport}, want: true},
		{name: "4xx", failure: &Failure{Kind: KindStatus, StatusCode: 400}},
		{name: "429", failure: &Failure{Kind: KindStatus, StatusCode: 429}},
		{name: "499", failure: &Failure{Kind: KindStatus, StatusCode: 499}},
		{name: "500", failure: &Failure{Kind: KindStatus, StatusCode: 500}, want: true},
		{name: "503", failure: &Failure{Kind: KindStatus, StatusCode: 503}, want: true},
		{name: "invalid call", failure: &Failure{Kind: KindInvalidCall}},
		{name: "decode", failure: &Failure{Kind: KindDecode}},
		{name: "canceled", failure: &Failure{Kind: KindCanceled}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DefaultAvailability(tt.failure); got != tt.want {
				t.Errorf("DefaultAvailability(%#v) = %t, want %t", tt.failure, got, tt.want)
			}
		})
	}
}
