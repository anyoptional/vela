package eurekaadapter

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	eureka "github.com/anyoptional/eureka-go"
	"github.com/anyoptional/vela"
)

type adapter struct {
	client *eureka.Client
}

// New adapts an eureka-go client to vela service discovery. Logging stays
// with the eureka-go client's own logger (logr to stdout by default).
func New(client *eureka.Client) vela.DiscoveryClient {
	return &adapter{client: client}
}

func (a *adapter) Description() string {
	return "eureka-go"
}

func (a *adapter) Services(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.client == nil {
		return nil, errors.New("vela-eureka: client is nil")
	}
	return slices.Sorted(maps.Keys(a.client.Apps())), nil
}

func (a *adapter) Instances(ctx context.Context, serviceID string) ([]vela.Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.client == nil {
		return nil, errors.New("vela-eureka: client is nil")
	}

	discovered := a.client.InstancesOf(serviceID, eureka.WithStatus(eureka.UP))
	instances := make([]vela.Instance, 0, len(discovered))
	for _, discoveredInstance := range discovered {
		if instance, ok := mapInstance(discoveredInstance, serviceID); ok {
			instances = append(instances, instance)
		}
	}
	return instances, nil
}

func mapInstance(in *eureka.Instance, serviceID string) (vela.Instance, bool) {
	if in == nil || (!in.Port.Enabled && !in.SecurePort.Enabled) {
		return vela.Instance{}, false
	}

	host := in.IPAddr
	if host == "" {
		host = in.HostName
	}
	port := in.Port.Number
	if in.SecurePort.Enabled {
		port = in.SecurePort.Number
	}

	var metadata map[string]string
	if values := in.Metadata.GetMap(); values != nil {
		metadata = make(map[string]string, len(values))
		for key, value := range values {
			metadata[key] = fmt.Sprint(value)
		}
	}

	return vela.Instance{
		ID:        in.Id(),
		ServiceID: serviceID,
		Host:      host,
		Port:      port,
		Secure:    in.SecurePort.Enabled,
		Metadata:  metadata,
	}, true
}

var _ vela.DiscoveryClient = (*adapter)(nil)
