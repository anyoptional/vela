# jinx：基于 Eureka 的负载均衡 HTTP 客户端 — 设计

日期：2026-11-15

## 意图与动机

jinx 是发布出去给别人用的 Go 库：服务间 HTTP 调用时，按服务名从注册中心发现实例、客户端侧选路、失败自动转移，HTTP 传输基于 go-resty v3。目标用户分两类：

- go-zero 生态用户：有 `.api` 文件，想要强类型的客户端代码。
- 任意带 Eureka 的 Go 服务：想要一个运行时负载均衡 HTTP 客户端。

为此 jinx 分三个模块，由 Go workspace 管理。

## 目标

1. **core 模块**：eureka 感知的负载均衡 HTTP 客户端。公开 API 完全使用 jinx 自有类型，resty v3 是纯内部依赖，不出现任何公开签名中。
2. **eureka 适配器模块**：基于 eureka-go 实现 core 的 `DiscoveryClient` 接口，完成 `eureka.Instance` 到 core `Instance` 的映射。
3. **goctl 插件模块**：解析 `.api` 文件，为每个服务生成 interface + 默认实现（实现调 core）。

## 非目标（v1 不做）

- weighted 负载均衡（metadata 权重）
- 推送式 watch 模型
- composite discovery（多 DiscoveryClient 组合链，及配套的 `Order()`）
- 服务注册（eureka-go 已覆盖；jinx 只管发现 + 调用）
- 熔断器（circuit breaker）。v1 只有短冷却
- SSE / multipart / cookie 等 resty 高级功能的再暴露（有需求时按个加）

## 反目标（做了就算失败的）

- resty v3 RC 的类型穿透进公开 API — RC→stable 升级必须对消费者零影响
- go-zero / goctl 依赖进入 core
- 生成代码直接 import resty（必须只 import core）

## 约束

- Go 1.25，仓库根 `go.work`，三个独立 go.mod 模块
- resty v3：`resty.dev/v3`，当前 v3.0.0-rc.4（stable 未发布）。仅 core 依赖
- eureka-go v1.0.0：`Client.InstancesOf(name, WithStatus(UP))` 拉内存缓存快照；`Instance` 有 `IPAddr`、`HostName`、`Port.Number`、`SecurePort.Number`、`Metadata`
- goctl 插件协议：插件二进制经 stdin 接收 `plugin.NewPlugin()`，拿 `Api *spec.ApiSpec`、`Dir`、`Style`；需依赖 `github.com/zeromicro/go-zero/tools/goctl/plugin`（大依赖只压在插件二进制上）
- 仓库现状：jinx 目录为空仓库（无 .go 文件，无 git），eureka-go 源码在旁目录 `D:/Codes/WorkspaceGo/eureka`

## Approaches（已讨论并选定）

| 方案 | 权衡 | 结论 |
|---|---|---|
| A. 纯 goctl 插件 | 强类型，但受众锁死 go-zero；插件协议跨版本断裂由库承担；resty RC 写进每个生成项目 | 否 |
| B. 纯运行时库 | 无 go-zero 耦合，但无类型 | 否 |
| C. 核心库 + 可选插件 | 两类用户都覆盖；goctl 断裂只伤插件；resty RC 只存在于 jinx 内部 | **选** |

子决策（均已选定）：

- **Discovery 模型**：拉取快照（`Instances(ctx, serviceID)`），不做 watch。每请求要的就是当前一份列表；本地冷却补掉过期实例。
- **core 公开 API**：自有 Request 构建器（链式），resty 只藏在 `Do` 内部。否决 resty 风格链式（类型穿透）和薄函数式（无处放扩展）。
- **代码生成形态**：每个 api 文件一个 service 块 → 生成一个 interface（interface 名 = service 块名），service 内全部 route 方法都在这个接口里；`@server` 只是路由分组，提供 prefix 拼路径，不产生独立类型。方法名取 `@handler`。

## 接口（已定）

```go
// core 模块，包名待定（jinx）

type Instance struct {
    ID        string
    ServiceID string
    Host      string
    Port      int
    Secure    bool   // true 走 https://host:securePort，false 走 http://host:port
    Metadata  map[string]string
}

// 参照 Spring DiscoveryClient，砍掉 probe() 与 getOrder()
//（probe 是 Spring HealthIndicator 专用；order 是 composite 链专用，v1 无链）
type DiscoveryClient interface {
    Description() string
    Instances(ctx context.Context, serviceID string) ([]Instance, error)
    Services(ctx context.Context) ([]string, error)
}

// 参照 Spring Cloud 现代架构：ServiceInstanceListSupplier（core 负责拉列表、
// 过滤 tried/冷却后传入 candidates）+ 无状态 Chooser。改名为 LoadBalancer。
type LoadBalancer interface {
    Choose(ctx context.Context, serviceID string, candidates []Instance) (Instance, error)
}
```

内置实现：只有 round-robin（每服务计数器 + 互斥锁），作为内部默认，不公开构造函数；自定义策略走 `WithLoadBalancer` 选项。错误语义：候选为空返回 typed error `ErrNoInstances`。

## 运行时行为（已定）

- **选路**：每请求从 Discovery 拉一次快照；每次尝试 core 过滤 tried-set + 冷却实例后传入候选，`Choose` 选一个。
- **失败转移**：连接错误 / 5xx → 换下一实例重试；4xx 不转移。同一请求内不重试同一实例两次。转移次数上限 = 重试配置。
- **ErrNoInstances**：discovery 返回空、或过滤后候选为空时 core 返回；内置 LB 收到空候选也返回同一错误。
- **冷却**：失败实例本地短冷却（默认 10s，可配），冷却状态放 core 客户端，`LoadBalancer` 保持纯策略。**只有传输/连接错误进冷却**；5xx 只触发同请求内转移，不进冷却（5xx 可能是单实例偶发，冷却会造成单实例服务 10s 黑洞）。冷却实例是"降权"不是"禁用"：若全部候选都在冷却，照样尝试（先试非冷却）。
- **端口与 scheme**：`Secure = SecurePort.Enabled`（Spring 注册惯例：即使纯 HTTP 服务也常带 `securePort.Enabled=false`，按 `Number > 0` 判 secure 会把流量打到 `https:443`）。secure 启用走 `SecurePort.Number`，否则走 `Port.Number`；`Port.Enabled=false` 视为该端口不可用。两个端口都不可用的实例由 adapter 直接过滤掉，不进候选。
- **resty 重试**：禁用 resty 自带重试。跨实例转移要每次换 URL，转移循环由 core 自己做，每实例一次尝试。
- **状态过滤**：eureka adapter 在 `Instances()` 里过滤（只返回 UP），core 不做状态判断。

## eureka 适配器（已定）

- `IPAddr` 优先，空则 `HostName`；端口用 `Port.Number`（secure 用 `SecurePort`）。
- zone 放入 `Metadata`（key 如 `zone`），留给将来。
- 日志：eureka-go 用 logr；adapter 负责桥接（core 用 slog，见"留给构建者"）。

## goctl 插件（已定）

- 输入：goctl 解析好的 `spec.ApiSpec`。
- 输出：api 文件 service 块 → 一个 interface + 一个默认实现（调用 core）。interface 名 = service 块名。
- 方法：service 内每个 route 一个方法，`@handler` 名做方法名；path 参数（`/orders/:id`）成为方法入参，经 core 的 `SetPathParam` 传入；`@server` 的 prefix 拼进路径。
- **serviceID**：discovery 查询用的 serviceID = service 块名（固定契约，无 flag）；adapter 精确匹配，不做大小写归一化（文档写明：Eureka app 名必须与 service 块名一致）。
- 类型：插件**不生成**请求/响应类型 — 类型由 goctl 自身生成（`goctl api go` 的 types，位于 `<module>/internal/types`），生成代码只 import。插件解析目标目录的 go.mod 推导 import 路径 `<module>/internal/types`；非标准布局直接报清晰错误。
- **插件零参数**：Dir/Style 走 goctl payload；serviceID = service 块名（固定契约，见下）；types 路径从 go.mod 推导。
- 插件不 import resty。

## 模块布局（已定）

```
jinx/                仓库根，go.work
├── core 源码        module github.com/anyoptional/jinx
├── eureka/          module github.com/anyoptional/jinx-eureka
└── goctl/           module github.com/anyoptional/jinx-goctl
```

## 留给构建者（构建者的调用，未与作者确认）

- **日志**：core 用标准库 `slog`（Go 1.25 标准），`WithLogger` 可注入；eureka adapter 做 logr→slog 桥接。
- **配置面**：`New(discovery, opts...)`，选项：`WithLoadBalancer`（缺省 = 内部 round-robin）、`WithTimeout`（单次尝试超时；总预算走 ctx deadline）、`WithMaxAttempts`、`WithCooldown`（默认 10s）、`WithLogger`、`WithHTTPClient`（自定义 `*http.Client`，覆盖 mTLS 证书等场景；自定义 CA 可用 `SSL_CERT_FILE`、代理走环境变量）。User-Agent 硬编码 `jinx`，单请求用 `SetHeader` 覆盖。
- **Request 构建器**：`NewRequest(serviceID, method, path string)`、`SetPathParam`、`SetQueryParam`、`SetHeader`、`SetBody`（JSON）、`SetResult`、`Do(ctx)`。**Do 契约**：收到任何 HTTP 响应（含 4xx/5xx）都返回非 nil Response；非 2xx 时 error 非 nil，携带状态码 + 有界 body 摘录，完整 body 在 `Response.Body`，调用方自行解码（无 `SetError`）。
- **Response**：`{ StatusCode int, Body []byte }`，两个字段。
- **错误类型目录**（两个公开类型）：`ErrNoInstances`（sentinel，含 discovery 为空与过滤后为空）；`*TransportError`（resty→jinx 边界适配器，`Unwrap` 只暴露消毒后的 cause）。发现失败 = `fmt.Errorf("discover %q: %w", ...)`；重试耗尽 = 最后一次尝试的错误 + 尝试次数入 message。
- **插件参数**：零参数。
- **测试策略**：单元 + `-race` + live 集成测试（对照 eureka-go 的 `live_integration_test.go` 模式，环境变量开关）。
