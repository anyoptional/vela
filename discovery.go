package jinx

import "context"

// DiscoveryClient provides service and instance snapshots.
type DiscoveryClient interface {
	Description() string
	Instances(ctx context.Context, serviceID string) ([]Instance, error)
	Services(ctx context.Context) ([]string, error)
}
