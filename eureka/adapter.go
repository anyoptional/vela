package eurekaadapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	eureka "github.com/anyoptional/eureka-go"
	"github.com/anyoptional/jinx"
	"github.com/go-logr/logr"
)

type adapter struct {
	client *eureka.Client
}

// New adapts an eureka-go client to jinx service discovery.
func New(client *eureka.Client) jinx.DiscoveryClient {
	if client != nil {
		client.SetLogger(newSlogLogr(slog.Default()))
	}
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
		return nil, errors.New("jinx-eureka: client is nil")
	}
	return slices.Sorted(maps.Keys(a.client.Apps())), nil
}

func (a *adapter) Instances(ctx context.Context, serviceID string) ([]jinx.Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.client == nil {
		return nil, errors.New("jinx-eureka: client is nil")
	}

	discovered := a.client.InstancesOf(serviceID, eureka.WithStatus(eureka.UP))
	instances := make([]jinx.Instance, 0, len(discovered))
	for _, discoveredInstance := range discovered {
		if instance, ok := mapInstance(discoveredInstance, serviceID); ok {
			instances = append(instances, instance)
		}
	}
	return instances, nil
}

func mapInstance(in *eureka.Instance, serviceID string) (jinx.Instance, bool) {
	if in == nil || (!in.Port.Enabled && !in.SecurePort.Enabled) {
		return jinx.Instance{}, false
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

	return jinx.Instance{
		ID:        in.Id(),
		ServiceID: serviceID,
		Host:      host,
		Port:      port,
		Secure:    in.SecurePort.Enabled,
		Metadata:  metadata,
	}, true
}

type slogSink struct {
	logger *slog.Logger
	values []any
	name   string
}

func newSlogLogr(logger *slog.Logger) logr.Logger {
	if logger == nil {
		logger = slog.Default()
	}
	return logr.New(&slogSink{logger: logger})
}

func (s *slogSink) Init(logr.RuntimeInfo) {}

func (s *slogSink) Enabled(int) bool {
	return s.logger.Enabled(context.Background(), slog.LevelInfo)
}

func (s *slogSink) Info(_ int, message string, keysAndValues ...any) {
	s.logger.Info(message, s.logValues(keysAndValues)...)
}

func (s *slogSink) Error(err error, message string, keysAndValues ...any) {
	values := s.logValues(keysAndValues)
	if err != nil {
		values = append(values, "error", err)
	}
	s.logger.Error(message, values...)
}

func (s *slogSink) WithValues(keysAndValues ...any) logr.LogSink {
	values := append(slices.Clone(s.values), keysAndValues...)
	return &slogSink{logger: s.logger, values: values, name: s.name}
}

func (s *slogSink) WithName(name string) logr.LogSink {
	if s.name != "" && name != "" {
		name = s.name + "." + name
	} else if s.name != "" {
		name = s.name
	}
	return &slogSink{logger: s.logger, values: slices.Clone(s.values), name: name}
}

func (s *slogSink) logValues(keysAndValues []any) []any {
	values := make([]any, 0, len(s.values)+len(keysAndValues)+2)
	if s.name != "" {
		values = append(values, "logger", s.name)
	}
	values = append(values, s.values...)
	return append(values, keysAndValues...)
}

var _ jinx.DiscoveryClient = (*adapter)(nil)
