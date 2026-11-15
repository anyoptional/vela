# vela

vela 是一个 Go 客户端侧负载均衡 HTTP 库。它支持通过服务发现访问具名服务，或直接访问配置的 HTTP(S) URL；服务模式下可在实例间故障转移，并提供可定制的尝试策略与结构化故障信息。

仓库使用 Go workspace 管理三个独立模块：

| 模块 | 用途 |
| --- | --- |
| [`github.com/anyoptional/vela`](./go.mod) | 核心 HTTP 客户端、服务发现接口、负载均衡与故障转移。 |
| [`github.com/anyoptional/vela-eureka`](./eureka/README.zh-CN.md) | 将 `eureka-go` 适配到核心服务发现接口。 |
| [`github.com/anyoptional/vela-goctl`](./goctl/README.zh-CN.md) | goctl API 插件，生成基于核心客户端的强类型调用代码。 |

核心模块不依赖 Eureka 或 go-zero。适配器和代码生成器均为可选的独立模块。

## 核心库快速开始

### 服务发现

服务模式必须提供 `Target.Name` 和 `DiscoveryClient`。名称同时用作服务发现 ID 和逻辑客户端标识：

```go
func newOrdersClient(discovery vela.DiscoveryClient) (*vela.Client, error) {
	return vela.New(
		vela.Target{Name: "orders"},
		vela.WithDiscovery(discovery),
	)
}

func getHealth(ctx context.Context, client *vela.Client) error {
	var result struct {
		Status string `json:"status"`
	}
	_, err := client.NewRequest(http.MethodGet, "/health").
		SetResult(&result).
		Do(ctx)
	if err != nil {
		return err
	}
	slog.Info("服务健康状态", "status", result.Status)
	return nil
}
```

`DiscoveryClient` 由注册中心或应用自行实现；可通过 `WithLoadBalancer` 配置实例选择器。

### URL 直连

`Target.URL` 非空时，客户端直接访问该绝对 HTTP(S) URL，不需要 discovery，也不会使用负载均衡器。`Target.Name` 在直连模式下仍然必填：

```go
client, err := vela.New(vela.Target{
	Name: "orders",
	URL:  "http://127.0.0.1:8080",
})
if err != nil {
	return err
}

_, err = client.NewRequest(http.MethodGet, "/health").Do(ctx)
```

base URL 路径与 route path 合并；path 参数由请求构造器编码。目标 URL 不接受 query 或 fragment，查询参数应使用请求构造器设置。直连请求不调用 discovery/load balancer；传输失败后不会切换到服务发现，但可按尝试策略重试同一 URL。

### 请求与故障

`NewRequest(method, path)` 创建请求。请求构造器支持 path/query 参数、请求头、JSON 请求体（`SetBody`）、表单体（`SetFormParam`，编码为 `application/x-www-form-urlencoded`，与 `SetBody` 互斥）和响应结果解码。`Do` 返回收到的 HTTP `Response`（包括非 2xx）和错误；错误可通过 `errors.As` 识别为 `*vela.Failure`：

| 故障类别 | 含义 | 默认可用性故障 |
| --- | --- | --- |
| `KindInvalidCall` | 请求构造、编码或策略错误 | 否 |
| `KindDiscovery` | 注册中心错误或无实例 | 是 |
| `KindTransport` | 连接、传输、响应读取或单次尝试超时 | 是 |
| `KindStatus` | 非 2xx HTTP 状态 | 仅最终 5xx |
| `KindDecode` | 2xx 响应解码失败 | 否 |
| `KindCanceled` | 调用方取消或整体 deadline 到期 | 否 |

```go
var failure *vela.Failure
if errors.As(err, &failure) {
	slog.Error("请求失败", "kind", failure.Kind, "status", failure.StatusCode)
}
```

`Failure` 的 `Unwrap` 保留根因。core 包提供 `vela.DefaultAvailability` 谓词；强类型 fallback 动作由 goctl 生成层负责，参见 [goctl 插件指南](./goctl/README.zh-CN.md)。

## 尝试策略与超时

默认最多尝试 3 次，每次尝试默认超时 5 秒。默认策略只对传输故障和 5xx 重试或故障转移；服务模式优先切换到尚未尝试的实例，直连模式只重试同一 endpoint。`WithMaxAttempts` 和 `WithTimeout` 可覆盖这些上限。

超时分两层，request 默认继承 HTTP 客户端侧配置：

- **HTTP 客户端侧**：`WithTimeout` 设置客户端默认的单次尝试超时（整程上限：connect + 发送 + 读响应）；`WithConnectTimeout` 设置默认 `http.Client` 的 dial（连接建立）超时——Go 没有逐字节 read timeout，整程超时由 ctx deadline 承担。
- **Request 侧**：`Request.SetTimeout(d)` 覆盖本次请求的单次尝试超时；`d <= 0` 回退继承客户端默认（`WithTimeout`）。

每尝试超时经 `context.WithTimeout` 派生自调用方 ctx：子 deadline 自动取两者较小值，调用方总 deadline 贯穿全部尝试与实例转移（超时传递最佳实践）。调用方 context 的取消或整体 deadline 归类为 `KindCanceled`，不继续尝试、不启动 fallback；单次尝试超时归类为 `KindTransport`。

注意：`WithConnectTimeout` 只作用于默认 `http.Client`；传入 `WithHTTPClient` 或 `WithTransport` 后 dial 行为由调用方自管。

`WithAttemptPolicies` 接收 `AttemptPolicy` 函数切片，并**整体替换**内置默认策略链。策略按切片顺序运行；第一个返回非 `AttemptNext` 的策略决定动作。下面的例子只重试 429；由于默认策略已被替换，其他故障不会继承默认重试/故障转移：

```go
retry429 := func(
	_ context.Context,
	failure *vela.Failure,
	_ vela.AttemptState,
) (vela.AttemptAction, error) {
	if failure.Kind == vela.KindStatus && failure.StatusCode == http.StatusTooManyRequests {
		return vela.AttemptRetrySameEndpoint, nil
	}
	return vela.AttemptNext, nil
}

client, err := vela.New(
	vela.Target{Name: "orders", URL: "http://127.0.0.1:8080"},
	vela.WithMaxAttempts(3),
	vela.WithAttemptPolicies([]vela.AttemptPolicy{retry429}),
)
```

可用动作包括 `AttemptRetrySameEndpoint`、`AttemptFailover` 和 `AttemptStop`；`AttemptNext` 将决策交给下一项策略。core 仍会强制执行尝试次数、调用取消和请求体可重放性等约束。空策略切片表示不再尝试。
自定义策略也可以针对 4xx/429 或 `KindDecode` 决定重试、故障转移或停止；这些类别默认不会重试。尝试策略只控制 core 请求尝试，不会自动改变生成客户端的 fallback 可用性判断。

## 实例冷却与 Transport

默认冷却时长为 10 秒，可用 `WithCooldown` 配置；负值关闭冷却：

```go
client, err := vela.New(target,
	vela.WithDiscovery(discovery),
	vela.WithCooldown(10*time.Second),
)
```

冷却仅由传输错误触发，5xx 不会让实例进入冷却。冷却用于降低实例优先级，不是永久禁用；如果所有候选实例都处于冷却，客户端仍会尝试它们。

公开的 `Transport` 接缝可以替换整个 HTTP 往返；`WithHTTPClient` 则只替换底层 `http.Client`（例如代理或 TLS 配置）：

```go
roundTrip := func(ctx context.Context, req *http.Request) (*http.Response, error) {
	return http.DefaultTransport.RoundTrip(req.WithContext(ctx))
}

client, err := vela.New(target, vela.WithTransport(roundTrip))
```

自定义 transport 应返回响应及错误；响应体由 vela 读取并关闭。

## 接入 Eureka

配置并启动 `eureka-go` 客户端，再将其适配给 vela。Eureka 应用名必须与 `Target.Name` 中的服务 ID 一致：

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

client, err := vela.New(
	vela.Target{Name: "orders"},
	vela.WithDiscovery(eurekaadapter.New(eurekaClient)),
)
```

适配器只暴露状态为 `UP` 且至少有一个启用端口的实例。完整示例和真实环境集成测试说明见 [Eureka 适配指南](./eureka/README.zh-CN.md)。

## 使用 goctl 生成客户端

构建插件并将其放入 `PATH`：

```sh
cd goctl
go build -o goctl-vela .
```

服务由第三方实现时，只声明如何调用。插件自包含——**不要运行 `goctl api go`**（它会生成 server 侧 routes/handler/logic 等不需要的代码），直接一条命令：

```sh
goctl api plugin --plugin goctl-vela -api order.api -dir .
```

插件会生成 `<service>_api.go`（服务接口、强类型客户端配置、fallback 分派，文件名 = service 块名蛇形小写）和 `types/<service>_api.go`（导出子包，复用 goctl 的类型生成器）；`-dir` 可在模块子目录，types import 路径 = module path + 相对路径 + `/types`。预期用法是契约模块：api 文件放在独立模块里生成代码，消费方 import 该模块与 `<module>/types`。详细的服务发现、URL 直连、静态 fallback、fallback 工厂及自定义可用性示例见 [goctl 插件指南](./goctl/README.zh-CN.md)。

## 开发

仓库根目录的 workspace 管理 core、Eureka 和 goctl 模块。在各模块目录运行测试和 vet；workspace 会解析本地模块依赖。

子模块当前依赖 workspace 占位版本 `github.com/anyoptional/vela v0.0.0`。在发布子模块供 workspace 之外使用前，必须先将其更新为已发布的 core 版本。

核心模块当前在内部传输边界使用 Resty v3 RC-4；Resty 类型不会进入 vela 的公开 API。

## License

目前尚未选择许可证。公开发布仓库前必须先确定许可证。
