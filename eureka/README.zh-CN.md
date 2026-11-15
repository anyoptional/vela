# vela-eureka

`vela-eureka` 将 [eureka-go](https://github.com/anyoptional/eureka-go) 服务发现适配到 [`vela`](https://github.com/anyoptional/vela) 负载均衡 HTTP 客户端。

## 用法

```go
package main

import (
	"context"
	"log/slog"
	"net/http"

	eureka "github.com/anyoptional/eureka-go"
	"github.com/anyoptional/vela"
	eurekaadapter "github.com/anyoptional/vela-eureka"
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
		slog.Error("创建 Eureka 客户端", "error", err)
		return
	}
	if err := eurekaClient.Start(ctx); err != nil {
		slog.Error("启动 Eureka 客户端", "error", err)
		return
	}
	defer func() {
		if err := eurekaClient.Close(); err != nil {
			slog.Error("关闭 Eureka 客户端", "error", err)
		}
	}()

	client, err := vela.New(
		vela.Target{Name: "ORDERS"},
		vela.WithDiscovery(eurekaadapter.New(eurekaClient)),
	)
	if err != nil {
		slog.Error("创建 vela 客户端", "error", err)
		return
	}
	var health struct {
		Status string `json:"status"`
	}
	if _, err := client.NewRequest(http.MethodGet, "/health").
		SetResult(&health).Do(ctx); err != nil {
		slog.Error("请求 orders 服务", "error", err)
		return
	}
	slog.Info("服务健康状态", "status", health.Status)
}
```

`Target.Name` 必须与 Eureka 应用名一致。适配器只返回状态为 `UP` 且至少有一个启用端口的实例；安全端口启用时优先使用安全端口，并将元数据值转换为字符串。Eureka 的 `logr` 日志会桥接到默认 `slog` logger。

运行真实环境集成测试时，将 `VELA_EUREKA_URL` 设为可访问的 Eureka base URL，并在本模块执行 `go test ./... -run TestLive -v`。测试会注册一个临时实例，并在清理阶段注销。
