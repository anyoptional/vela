# jinx

jinx is a Go client-side load-balanced HTTP library. It discovers service instances through a pluggable interface, selects an instance for each request, and can fail over to another instance when a transport error or server error occurs.

The repository is a Go workspace with three independent modules:

| Module | Purpose |
| --- | --- |
| [`github.com/anyoptional/jinx`](./go.mod) | Core HTTP client, discovery interfaces, load balancing, and failover. |
| [`github.com/anyoptional/jinx-eureka`](./eureka/README.md) | Adapter from `eureka-go` to the core discovery interface. |
| [`github.com/anyoptional/jinx-goctl`](./goctl/README.md) | goctl API plugin that generates typed clients backed by the core. |

The core module does not depend on Eureka or go-zero. The optional adapter and generator are separate modules.

## Core quickstart

Implement `jinx.DiscoveryClient` for your registry, then pass it to `jinx.New`:

```go
type localDiscovery struct{}

func (localDiscovery) Description() string { return "local" }

func (localDiscovery) Instances(_ context.Context, serviceID string) ([]jinx.Instance, error) {
	return []jinx.Instance{{
		ID:        "orders-1",
		ServiceID: serviceID,
		Host:      "127.0.0.1",
		Port:      8080,
	}}, nil
}

func (localDiscovery) Services(context.Context) ([]string, error) {
	return []string{"orders"}, nil
}

func call(ctx context.Context) error {
	client := jinx.New(localDiscovery{})
	var result struct {
		Status string `json:"status"`
	}
	_, err := client.NewRequest("orders", http.MethodGet, "/health").
		SetResult(&result).
		Do(ctx)
	if err != nil {
		return err
	}
	slog.Info("service health", "status", result.Status)
	return nil
}
```

`Request` also supports path/query parameters, headers, JSON bodies, and per-request result decoding. Configure retry and transport behavior with options such as `WithMaxAttempts`, `WithTimeout`, `WithCooldown`, and `WithHTTPClient`.

## Eureka quickstart

Configure and start an `eureka-go` client, then adapt it for jinx. The Eureka application name must match the service ID passed to `NewRequest`.

```go
cfg := eureka.DefaultConfig()
cfg.Client.Zones = []eureka.ZoneURLs{{
	Zone: "default",
	URLs: []string{"http://localhost:8761/eureka/"},
}}
cfg.Client.Zone = "default"
cfg.Instance.App = "orders"
cfg.Instance.InstanceId = "orders-1"
cfg.Instance.HostName = "localhost"
cfg.Instance.IPAddr = "127.0.0.1"
cfg.Instance.Port = 8080
cfg.Instance.Status = eureka.UP

eurekaClient, err := eureka.New(cfg)
if err != nil {
	return err
}
if err := eurekaClient.Start(ctx); err != nil {
	return err
}
defer func() {
	if err := eurekaClient.Close(); err != nil {
		slog.Error("close Eureka client", "error", err)
	}
}()

client := jinx.New(eurekaadapter.New(eurekaClient))
```

The adapter exposes only `UP` instances with an enabled port. See the [Eureka adapter guide](./eureka/README.md) for a complete example and live-test instructions.

## goctl-generated clients

Build the plugin and make it available on `PATH`:

```sh
cd goctl
go build -o goctl-jinx .
```

In a go-zero API project, generate the regular types and the jinx client:

```sh
goctl api go -api order.api -dir .
goctl api plugin --plugin goctl-jinx -api order.api -dir .
```

The plugin emits `client.go` with an interface and a `jinx.Client` implementation. The service block name is used as the discovery service ID, and the module's `go.mod` is used to locate `internal/types`. Form/query parameters are rejected explicitly. See the [goctl plugin guide](./goctl/README.md).

## Development

The repository root is the workspace for the core, Eureka, and goctl modules. Run tests and vet from each module directory; the workspace resolves local module dependencies.

The child modules currently require the workspace placeholder `github.com/anyoptional/jinx v0.0.0`. Update that requirement to a released core version before publishing either child module for use outside this workspace.

The core currently uses Resty v3 RC-4 behind its internal transport boundary; Resty types are not part of jinx's public API.

## License

No license has been selected yet. Choose one before publishing this repository for public use.
