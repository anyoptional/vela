# jinx-goctl

`jinx-goctl` 是 goctl API 插件，用于生成强类型服务接口、jinx 客户端配置和 fallback 分派代码。插件会在指定目录生成 `client.go`。

## 构建与运行

构建插件，并将二进制文件放入 `PATH`：

```sh
cd goctl
go build -o goctl-jinx .
```

然后生成 go-zero 类型和 jinx 客户端：

```sh
goctl api go -api order.api -dir .
goctl api plugin --plugin goctl-jinx -api order.api -dir .
```

目标目录必须包含模块的 `go.mod`；生成器会使用其中的 module path 导入 `internal/types`。如果目录中已有非测试 Go 文件，生成文件使用这些文件的 package 名；否则默认使用 `client`。

service 块名会原样作为逻辑服务 ID，每个 `@handler` 会生成一个方法。path 参数根据对应 `path` tag 中声明的类型成为位置参数。请求和响应类型由 goctl 生成在 `internal/types`；GET 和 DELETE 方法不会发送请求体。暂不支持 form/query 参数，遇到时会明确返回错误。

## 构造生成客户端

生成器会为服务接口 `OrderApi` 生成 `OrderApiClientConfig` 和 `NewOrderApiClient`。无论服务发现还是 URL 直连，`Target.Name` 都必填：

```go
orders, err := client.NewOrderApiClient(client.OrderApiClientConfig{
	Target:        jinx.Target{Name: "orders"},
	Discovery:     eurekaadapter.New(eurekaClient),
	ClientOptions: []jinx.Option{jinx.WithTimeout(3 * time.Second)},
})
if err != nil {
	return err
}

order, err := orders.GetOrder(ctx, 42, &types.GetOrderReq{Id: 42})
```

当 `Target.URL` 为空时，必须在 `Discovery` 或 `ClientOptions` 中提供 `jinx.DiscoveryClient`；服务名必须与注册中心的 ID 一致。直连时设置绝对 HTTP(S) URL，不需要 discovery，且跳过负载均衡：

```go
orders, err := client.NewOrderApiClient(client.OrderApiClientConfig{
	Target: jinx.Target{
		Name: "orders",
		URL:  "https://orders.example.test",
		Path: "/api/v1",
	},
})
```

生成方法保留 route 的请求/响应类型与参数签名。`ClientOptions` 可配置 `WithMaxAttempts`、`WithAttemptPolicies`、`WithCooldown`、`WithTransport` 等 core 选项；`WithAttemptPolicies` 会整体替换默认尝试策略。更多策略与冷却语义见 [核心库 README](../README.zh-CN.md)。

## 静态 fallback

`Fallback` 必须实现生成的完整服务接口。下面假设 `OrderApi` 只包含 `GetOrder`；如果接口还有其他方法，fallback 也必须实现它们：

```go
type orderFallback struct{}

func (orderFallback) GetOrder(
	_ context.Context,
	id int64,
	_ *types.GetOrderReq,
) (*types.GetOrderResp, error) {
	return &types.GetOrderResp{Id: id}, nil
}

orders, err := client.NewOrderApiClient(client.OrderApiClientConfig{
	Target:    jinx.Target{Name: "orders", URL: "http://orders.example.test"},
	Fallback:  orderFallback{},
})
```

默认 `jinx.DefaultAvailability` 只对 discovery/空池、传输故障、单次尝试超时和最终 5xx 执行 fallback。HTTP 4xx（包括 429）、响应解码错误和调用方取消不会触发默认 fallback。fallback 直接以原调用参数执行，不会再次包装成 jinx 主客户端。

## fallback 工厂与自定义可用性

静态 `Fallback` 和 `FallbackFactory` 互斥，同时配置会导致客户端构造失败。工厂接收原始 `*jinx.Failure` 与 HTTP `*jinx.Response`；传输故障没有 HTTP 响应，因此 `Response` 为 nil。可用自定义谓词替换默认的故障分类：

```go
orders, err := client.NewOrderApiClient(client.OrderApiClientConfig{
	Target: jinx.Target{Name: "orders", URL: "http://orders.example.test"},
	FallbackFactory: func(
		ctx context.Context,
		failure *jinx.Failure,
		response *jinx.Response,
	) (client.OrderApi, error) {
		statusCode := 0
		if response != nil {
			statusCode = response.StatusCode
		}
		slog.ErrorContext(ctx, "使用订单 fallback",
			"kind", failure.Kind,
			"status", failure.StatusCode,
			"response_status", statusCode,
		)
		return orderFallback{}, nil
	},
	Availability: func(failure *jinx.Failure) bool {
		return jinx.DefaultAvailability(failure) ||
			(failure.Kind == jinx.KindStatus && failure.StatusCode == http.StatusTooManyRequests)
	},
})
```

`Availability` 是完整替换，不是自动叠加规则；如需保留默认分类，应显式调用 `jinx.DefaultAvailability`。应用也可以选择把解码错误等类别加入谓词。调用方 context 已取消时不会启动 fallback。

如果 fallback 方法本身返回错误，生成代码使用 `errors.Join` 同时保留原始 `*jinx.Failure` 和带有 `jinx: fallback` 上下文的 fallback 错误，可分别用 `errors.As` 与 `errors.Is` 检查。

## Windows

Windows 下请使用 `go build -o goctl-jinx.exe .`，并确保 goctl 能找到该可执行文件。
