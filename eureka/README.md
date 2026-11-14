# jinx-eureka

`jinx-eureka` adapts [eureka-go](https://github.com/anyoptional/eureka-go) service discovery to the [`jinx`](https://github.com/anyoptional/jinx) load-balanced HTTP client.

## Usage

```go
package main

import (
	"context"
	"log/slog"
	"net/http"

	eureka "github.com/anyoptional/eureka-go"
	"github.com/anyoptional/jinx"
	eurekaadapter "github.com/anyoptional/jinx-eureka"
)

func main() {
	ctx := context.Background()

	cfg := eureka.DefaultConfig()
	cfg.Client.Zones = []eureka.ZoneURLs{{
		Zone: "default",
		URLs: []string{"http://localhost:8761/eureka/"},
	}}
	cfg.Client.Zone = "default"
	cfg.Instance.App = "ORDERS"
	cfg.Instance.InstanceId = "orders-1"
	cfg.Instance.HostName = "localhost"
	cfg.Instance.IPAddr = "127.0.0.1"
	cfg.Instance.Port = 8080
	cfg.Instance.Status = eureka.UP

	eurekaClient, err := eureka.New(cfg)
	if err != nil {
		slog.Error("create Eureka client", "error", err)
		return
	}
	if err := eurekaClient.Start(ctx); err != nil {
		slog.Error("start Eureka client", "error", err)
		return
	}
	defer func() {
		if err := eurekaClient.Close(); err != nil {
			slog.Error("close Eureka client", "error", err)
		}
	}()

	client := jinx.New(eurekaadapter.New(eurekaClient))
	var health struct {
		Status string `json:"status"`
	}
	if _, err := client.NewRequest("ORDERS", http.MethodGet, "/health").
		SetResult(&health).Do(ctx); err != nil {
		slog.Error("request orders service", "error", err)
		return
	}
	slog.Info("service health", "status", health.Status)
}
```

The service ID passed to `NewRequest` must match the Eureka application name. The adapter returns `UP` instances with at least one enabled port, prefers the secure port when enabled, and converts metadata values to strings. It uses the default `slog` logger to bridge Eureka's `logr` output.

For a live integration test, set `JINX_EUREKA_URL` to a reachable Eureka base URL and run `go test ./... -run TestLive -v` from this module. The test registers a temporary instance and deregisters it during cleanup.
