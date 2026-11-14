# jinx

jinx 是一个 Go 客户端侧负载均衡 HTTP 库。它通过可插拔接口发现服务实例，为每次请求选择实例，并可在传输错误或服务端错误时切换到其他实例。

仓库使用 Go workspace 管理三个独立模块：

| 模块 | 用途 |
| --- | --- |
| [`github.com/anyoptional/jinx`](./go.mod) | 核心 HTTP 客户端、服务发现接口、负载均衡与故障转移。 |
| [`github.com/anyoptional/jinx-eureka`](./eureka/README.zh-CN.md) | 将 `eureka-go` 适配到核心服务发现接口。 |
| [`github.com/anyoptional/jinx-goctl`](./goctl/README.zh-CN.md) | goctl API 插件，生成基于核心客户端的强类型调用代码。 |

核心模块不依赖 Eureka 或 go-zero。适配器和代码生成器均为可选的独立模块。

## 核心库快速开始

为注册中心实现 `jinx.DiscoveryClient`，再传给 `jinx.New`：

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
	slog.Info("服务健康状态", "status", result.Status)
	return nil
}
```

`Request` 还支持 path/query 参数、请求头、JSON 请求体和响应结果解码。可通过 `WithMaxAttempts`、`WithTimeout`、`WithCooldown`、`WithHTTPClient` 等选项配置重试和传输行为。

## 接入 Eureka

配置并启动 `eureka-go` 客户端，再将其适配给 jinx。Eureka 应用名必须与传给 `NewRequest` 的服务 ID 一致。

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
		slog.Error("关闭 Eureka 客户端", "error", err)
	}
}()

client := jinx.New(eurekaadapter.New(eurekaClient))
```

适配器只暴露状态为 `UP` 且至少有一个启用端口的实例。完整示例和真实环境集成测试说明见 [Eureka 适配指南](./eureka/README.zh-CN.md)。

## 使用 goctl 生成客户端

构建插件并将其放入 `PATH`：

```sh
cd goctl
go build -o goctl-jinx .
```

在 go-zero API 项目中，先生成常规类型，再生成 jinx 客户端：

```sh
goctl api go -api order.api -dir .
goctl api plugin --plugin goctl-jinx -api order.api -dir .
```

插件会生成包含接口和 `jinx.Client` 实现的 `client.go`。service 块名会作为服务发现 ID，模块的 `go.mod` 用于定位 `internal/types`。遇到 form/query 参数时会明确报错，不会静默丢弃。详见 [goctl 插件指南](./goctl/README.zh-CN.md)。

## 开发

仓库根目录的 workspace 管理 core、Eureka 和 goctl 模块。在各模块目录运行测试和 vet；workspace 会解析本地模块依赖。

核心模块当前在内部传输边界使用 Resty v3 RC-4；Resty 类型不会进入 jinx 的公开 API。

## License

目前尚未选择许可证。公开发布仓库前必须先确定许可证。
