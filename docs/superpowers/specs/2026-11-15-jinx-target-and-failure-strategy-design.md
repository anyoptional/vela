# vela Target 与故障策略——设计方案（评审修订版）

日期：2026-11-15

状态：已按架构评审裁决修订，并经用户审核批准执行（评审报告 2026-11-15，裁决：除“接缝普查”外全采纳，接缝保留公开）。

## 目标与背景

围绕显式请求目标和可组合的故障处理，重新设计 vela 草稿版客户端。客户端既能通过服务发现和负载均衡调用具名服务，也能直接访问配置的 URL。生成的强类型客户端需要支持静态 fallback 实现和 fallback 工厂，同时允许应用编写比 `fallbackOnError` 布尔开关更丰富的错误处理逻辑。

本设计不保留当前草稿 API 的兼容性。仓库尚未发布，可以调整公开构造函数和请求签名。

## 评审裁决记录（2026-11-15）

架构评审结论：原方案公开面约翻倍、v1 新增接缝多为假设性。裁决如下，本修订版全文落实：

1. **策略链收窄**：放弃有序策略链类型体系（接口、链类型、插入/重排方法、per-request 层）。attempt 策略 = 一个函数类型 + 一个切片选项，整体替换默认策略；per-request 策略层砍掉（Feign 亦无此层）。
2. **FailureKind 11 → 6**：合并 message-only 分类；pre-flight 目标/配置错误走构造函数普通错误，不产生 Failure。
3. **fallback 布线分层**：core 只提供 `DefaultAvailability` 谓词；有类型 fallback 动作只在生成层；fallback 失败用 `errors.Join`，不设专用错误类型。
4. **本地冷却回归**：恢复旧设计钉死的冷却语义（原文档静默丢失，属回归）：只有传输错误进冷却、冷却=降权不是禁用、全部冷却时照样尝试、5xx 永不冷却、默认 10s、`WithCooldown` 可配。
5. **Target 单构造**：`New(Target, opts...)`，不设双构造函数；URL 为空时 `WithDiscovery` 必填。
6. **Failure 瘦身**：公开 `{Kind, StatusCode, Err}` + `Error()`/`Unwrap()`/`LogValue()`；可观测字段不导出，由内部脱敏格式化器统一处理；摘录上限 512B 为内部常量。
7. **接缝公开**（作者裁决：实际项目会自定义，全部保留公开）：`Transport` 函数类型 + `WithTransport`；`LoadBalancer`、`AttemptPolicy`、`DiscoveryClient` 公开；fallback 配置在生成层公开。
8. **继承清单**：`ErrNoInstances` sentinel、resty 错误链消毒（unwrap 链不含 resty 类型）、Do 契约（收到任何响应都返回非 nil Response）、默认值（3 次尝试、5s 单次超时、UA `vela`）、插件 CLI 零参数、旧 review-focus 测试重映射。

## 目标

1. 明确区分服务发现调用和 URL 直连两种目标模式。
2. 提供有类型、可检查的错误分类，并为应用策略保留足够的尝试及响应上下文。
3. 以有序策略链组合错误处理和恢复行为（策略 = 函数类型切片，见裁决 1）。
4. 默认仅在重试/故障转移策略耗尽后，对可用性故障执行强类型 fallback。
5. 允许调用方添加、替换、重排自定义错误策略，同时保留原始原因和响应信息。
6. 在生成的服务接口层提供类型安全的静态 fallback 和 fallback 工厂。
7. 清晰区分服务端/应用层错误与客户端可用性故障。

## 非目标

- Spring 容器概念，例如 `contextId`、`qualifier`、Bean 注册或 `primary`。
- 在 core 包中动态解析 `${property}`；调用方应在构造目标前解析配置。
- 直连 URL 失败时静默切换到服务发现。
- 第一版实现熔断器、并行请求（hedged request）或分布式冷却状态。
- 把所有非 2xx 响应都视为可用性故障。
- 仅为支持 URL 直连而修改 Eureka adapter 的服务发现契约。
- core 内的通用 recovery 链（有类型恢复在 core 必须用 `any`，破坏类型安全；恢复动作在生成层，见裁决 3）。
- per-request 策略层（Feign 亦无，见裁决 1）。

## 反目标

- 只增加 `fallbackOnError bool` 作为唯一自定义方式。
- 使用吞掉原始错误或伪造成功响应的兜底处理器。
- 在重试/故障转移耗尽前执行 fallback。
- 在错误字符串或日志中泄漏未限长的响应体、凭据或 URL userinfo。
- 直连 URL 仍必须提供 `DiscoveryClient`。
- 让强类型 fallback 要求 core 导入生成的服务包。
- resty 类型出现在任何公开签名或错误解包链中。

## 方案比较

| 方案 | 权衡 | 决策 |
|---|---|---|
| 在每个生成方法中直接编写 `if err != nil { fallback }` | 初期简单，但会重复语义，无法可靠分类故障，策略顺序和自定义行为也不明确。 | 不采用 |
| 只在 core 中 fallback 并返回原始 HTTP 响应 | 符合 core 的 `Response`，但无法自然实现生成接口上的强类型 fallback 方法或工厂。 | 不作为 fallback 层 |
| 分层策略链并与强类型客户端集成 | Core 负责分类、冷却与单次尝试层策略（函数类型）；生成的强类型客户端用 `DefaultAvailability` 谓词触发有序 fallback。职责清晰、顺序可扩展。 | **采用**（按评审裁决收窄后） |

## Target 模型

### 对外语义

- `Name` 标识逻辑客户端；无论何种模式都必填。
- `URL` 为空时使用服务发现：`Name` 作为服务 ID，由 `WithDiscovery` 配置的 `DiscoveryClient` 解析实例，再由 `LoadBalancer` 选择实例。
- `URL` 非空时使用直连：请求访问该绝对 `http` 或 `https` URL，跳过服务发现和负载均衡。`Name` 仍作为错误、指标和生成客户端使用的逻辑客户端标识。
- URL 传输失败后不得静默切换到服务发现。
- 客户端级路径前缀由 api 文件的 `@server` 注解维护（生成层把 prefix 拼进各 route path）；core 的 `Target` 不设 Path 字段。Query 参数属于请求参数，不属于 target URL。
- 缺少名称、URL 格式错误、scheme 不支持或路径配置无效，都必须在构造时、发起网络请求前报错（普通 `error`，不产生 `Failure`）。

### 构造

```go
type Target struct { Name, URL string }

func New(t Target, opts ...Option) (*Client, error) // 校验失败返回普通 error
func WithDiscovery(DiscoveryClient) Option          // URL == "" 时必填；URL != "" 时忽略
```

- 不设双构造函数：单构造把 mode 分支藏在 core 里一次，生成模板不必在每个调用点分支。
- 不通过 discovery 是否为 nil 推断直连模式：URL 为空而 discovery 缺失 = 校验错误。

### 内部 endpoint 解析

- 服务 target 调用服务发现并得到零个或多个实例 endpoint。
- 直连 target 从配置的 URL 得到且只得到一个 endpoint。
- 负载均衡和实例故障转移只适用于服务 target。直连模式可以按重试策略重试同一 endpoint，但不能把一个 URL 当成实例集合。
- 两种模式共用 HTTP transport、请求编码、响应读取、超时和错误分类逻辑。

## 错误模型

### 公开面

```go
type Kind uint8

const (
    KindInvalidCall Kind = iota // 请求构造/编码、实例选择、策略缺陷等调用方问题
    KindDiscovery               // 注册中心失败或空池；空池以 ErrNoInstances 为 cause
    KindTransport               // 连接/DNS/TLS 失败、单次尝试超时、响应读取失败
    KindStatus                  // 非 2xx；StatusCode 区分 4xx/5xx/429
    KindDecode                  // 2xx 但响应体解码失败
    KindCanceled                // 调用方取消或整体 deadline 到期（区别于单次尝试超时）
)

type Failure struct {
    Kind       Kind
    StatusCode int
    Err        error
}
// Error() / Unwrap() / LogValue()；可观测字段（客户端名、target 模式、尝试号、
// 实例 ID、脱敏 endpoint、响应摘录）不导出，由内部格式化器统一脱敏输出。
// 摘录上限 512B 为内部常量；不得泄漏 URL userinfo 或敏感 query。

var ErrNoInstances = errors.New("vela: no instances available")
```

- pre-flight 目标/配置错误在构造函数直接返回普通 `error`，不产生 `Failure`。
- `Failure` 必须支持 `errors.Is`/`errors.As` 并保留根因；`KindDiscovery` 空池场景 cause 为 `ErrNoInstances`（继承旧设计 RF4 区分）。
- resty 边界：错误链消毒继承旧反目标——`Failure.Unwrap` 链中不得出现 resty 包类型（旧计划 Task 5 的消毒测试保留）。
- 调用方取消（`KindCanceled`）必须能与单次尝试超时（`KindTransport`）区分。

### 默认可用性分类

- **默认属于可用性故障：** 服务发现失败、无 endpoint、传输失败、响应读取失败、单次尝试超时，以及重试/故障转移预算耗尽后的最终 HTTP 5xx。
- **默认不属于可用性故障：** 请求构造/编码错误（`KindInvalidCall`）、HTTP 4xx（含 429）、解码错误（`KindDecode`）、调用方取消/整体 deadline（`KindCanceled`）。
- core 导出单一谓词：

```go
func DefaultAvailability(f *Failure) bool
```

- 应用可替换该判断：生成层提供 `WithXxxAvailability(func(*Failure) bool)` 选项（默认 `vela.DefaultAvailability`）；core 裸调用方自行写 `errors.As` 分支。

## 尝试策略（core）

### 契约

```go
type AttemptAction uint8

const (
    AttemptNext               AttemptAction = iota // 不匹配，交下一策略
    AttemptRetrySameEndpoint                        // 重试同一 endpoint
    AttemptFailover                                 // 换未尝试过的服务实例（直连模式无效，等同 Next）
    AttemptStop                                     // 停止尝试
)

type AttemptState struct {
    Attempt          int        // 当前尝试序号（从 1 起）
    AttemptLimit     int        // 配置的尝试上限
    UntriedEndpoints int        // 剩余未尝试实例数（直连模式恒 1）
    TargetMode       TargetMode // 服务发现或直连
    Replayable       bool       // 请求体是否可重放
}

type AttemptPolicy func(ctx context.Context, f *Failure, s AttemptState) (AttemptAction, error)

func WithAttemptPolicies([]AttemptPolicy) Option // 整体替换默认策略链
```

- 顺序组合：core 按切片顺序执行；第一个非 `AttemptNext` 的动作生效；全部 `AttemptNext` 则停止。
- `WithAttemptPolicies` 整体替换内置默认策略（retry-same + failover-next 的默认集）；插入/重排/删除 = 调用方切切片，不提供链方法。
- 无 per-request 策略层（评审裁决 1）。
- 策略自身出错：包装为 `KindInvalidCall` 的 `Failure`，保留原 `Failure` 为 cause。
- 安全约束由 core 强制执行，策略无法突破：尝试上限、请求体可重放性、调用方取消。直连模式只允许 `AttemptRetrySameEndpoint`。
- 默认策略：传输/超时、响应读取失败与 5xx 可重试或转移；4xx、`KindInvalidCall`、`KindDecode`、`KindCanceled` 为终止。

### 本地冷却（评审裁决 4，恢复旧设计语义）

- 只有传输错误（`KindTransport`）把实例放入冷却；5xx 永不冷却。
- 冷却 = 降权不是禁用：候选过滤先剔除冷却实例；若全部候选都在冷却，照样全部尝试。
- 默认冷却时长 10s，`WithCooldown(d)` 可配（`d < 0` = 关闭）。
- 冷却状态为跨请求的本地状态，挂在 core Client 上，单次请求不共享可变状态（冷却表本身并发安全）。

### Transport 接缝（评审裁决 7）

```go
type Transport func(ctx context.Context, req *http.Request) (*http.Response, error)

func WithTransport(Transport) Option // 覆盖默认 resty transport
```

- 与 `WithHTTPClient` 分工：前者替换整个往返（测试、拦截），后者只替换底层 `http.Client`（TLS/代理）。

### 日志

- 三个模块统一使用 go-logr；core 默认 `logr.FromSlogHandler(slog.Default().Handler())` 输出到 stdout，`WithLogger(logr.Logger)` 可注入。
- 不自定义 logger（如无必要）：eureka 适配器沿用 eureka-go 自带 logr 默认（stdout），插件无日志输出（错误走 stderr 协议通道）。resty 的 Logger 接口由最小 logr 桥接（必要适配，非自定义实现）。

### 可观测性（OpenTelemetry）

- 默认 `http.Client` 由 `otelhttp.NewTransport` 包装：每次 HTTP 调用产生一个 client span（每尝试一个），W3C trace context 自动注入出站请求，span 为调用方 span 的子级；使用全局 `TracerProvider`/`Propagators`（未注册时 no-op，零配置开销）。
- `WithHTTPClient`/`WithTransport` 注入后由调用方自行接 otel（vela 不替自定义往返加插桩）。
- resty v3 rc.4 无内置 otel；官方推荐即 `otelhttp.NewTransport`，vela 按此接入，不引第三方 resty 插桩库。

### 超时分层

- **HTTP 客户端侧**：`WithTimeout` = 客户端默认单次尝试超时（整程：connect + 发送 + 读响应）；`WithConnectTimeout` = 默认 `http.Client` 的 dial 超时（Go 无逐字节 read timeout，整程由 ctx deadline 承担）。`WithConnectTimeout` 只在默认 `http.Client` 生效；`WithHTTPClient`/`WithTransport` 注入后 dial 行为归调用方。
- **Request 侧**：`Request.SetTimeout(d)` 覆盖单次尝试超时；`d <= 0` 继承客户端默认。
- 每尝试超时经 `context.WithTimeout` 派生自调用方 ctx（子 deadline 自动取小），调用方总 deadline 贯穿全部尝试与转移；resty 客户端不再设自身超时（避免 request 放宽被客户端默认卡死），deadline 全权由 attemptCtx 承担。

## 生成层的强类型 fallback

### 触发与布线（评审裁决 3）

- core 负责触发规则，生成层负责值。生成模板只写一条路径：

```go
resp, err := req.Do(ctx)
var f *vela.Failure
if err != nil && errors.As(err, &f) && availability(f) { /* fallback */ }
```

- 模板不得 switch `Failure.Kind`——kind 规则只存在于 core 的谓词中，测试一次，不在每个生成服务里复制。

### 生成客户端配置

```go
type ${Service}ClientConfig struct {
    Target         vela.Target
    Discovery      vela.DiscoveryClient      // 服务 target 必填
    ClientOptions  []vela.Option
    Fallback       ${Service}                // 静态 fallback，实现生成接口
    FallbackFactory func(context.Context, *vela.Failure, *vela.Response) (${Service}, error)
    Availability   func(*vela.Failure) bool  // 默认 vela.DefaultAvailability
}

func New${Service}Client(config ${Service}ClientConfig) (${Service}, error)
```

- 静态 `Fallback` 与 `FallbackFactory` 互斥：同时配置 = 构造错误（评审裁决，明确优于隐式优先级）。
- `FallbackFactory` 接收 `*Failure` 与 `*Response`（传输失败时 `Response` 为 nil）；一次给全，日后不破坏签名。
- fallback 实现本身不会被递归包装进主客户端；其方法按原参数直接调用。
- fallback 方法自身失败：`errors.Join(原始 Failure, fmt.Errorf("vela: fallback: %w", fbErr))`，两类错误均可分别检查，不设专用错误类型。
- 插件同时生成 `types/<service>_api.go`（复用 goctl `gogen.BuildTypes`，输出与 `goctl api go` 一致）。第三方服务场景只声明如何调用，**不需要先运行 `goctl api go`**——其生成的 server 侧 routes/handler/logic 等代码没有用途。生成文件名为 `<service>_api.go`（service 块名蛇形小写），不同服务的 api 文件生成各自文件；`-dir` 可在模块子目录，types import 路径 = module path + 相对路径 + `/types`（向上查找最近 go.mod）。
- **Target 元数据来自 api 文件**：`Target.Name` = service 块名（固定契约）；`@server` 注解可带 `url` key（如 `url: "http://orders.test"`），所有组的 url 必须一致、冲突生成期报错，非空即直连模式。生成代码内建 `defaultTarget`，`config.Target` 留作部署期覆盖（零值 Name → 用 api 文件派生的 Target）。
- 生成方法接收单个请求结构体，字段按 goctl tag 分区：`path` → 路径参数（标量，与 route `:name` 一一对应）、`header` → 请求头（标量）、`form` → GET/DELETE/HEAD/OPTIONS 走 query、其余方法走 `application/x-www-form-urlencoded` body（标量；core 的 `SetFormParam` 与 `SetBody` 互斥）、`json` 或无 tag → JSON body（body 只含 json 字段）。同一请求类型混用 json 与 form 字段 → 生成期报错；json+path+header 混用允许。
- types 放在**导出的** `types` 子包而非 `internal/types`：契约模块会被其他模块 import，Go 的 internal 规则禁止模块外导入 internal 包；消费方同时 import 契约模块（接口）与 `<module>/types`（请求/响应类型）。

## 请求与响应行为

- 两种 target 模式共用 path 参数替换、query、header、请求体、超时及响应 API。
- 所有已接收的 HTTP 响应都可检查，包括非 2xx；`Do` 契约：收到任何响应都返回非 nil `Response`（`{StatusCode, Body []byte}`）。
- 4xx 默认不重试，也不触发默认 fallback；自定义策略（attempt 策略或 `WithXxxAvailability`）可以选择恢复。
- 最终 5xx 在重试/故障转移耗尽后，默认属于可 fallback 的可用性故障。
- 解码错误与传输/状态错误区分；默认不触发 fallback。
- 调用方 context 被取消时，不启动 fallback 网络请求；执行本地工作的 fallback 收到的 context 仍须遵守文档规定的取消语义。

## 验证要求

实施计划必须安排测试证明：

1. 服务 target 通过 discovery 解析，并能在多个实例间负载均衡/故障转移；同一请求不重试同一实例两次。
2. 直连 URL 在没有 discovery 依赖时可用，且绝不调用 discovery 或 load balancer。
3. 直连模式下 `Name` 仍必填；错误或不支持的 URL 在构造时、网络请求前失败（普通 error）。
4. Base URL 路径、配置前缀、route path、path 参数和 query 能正确组合及转义；缺 path 参数在网络请求前报错。
5. 每个故障边界产生预期的 6 类 `FailureKind` 之一，保留 `errors.Is`/`errors.As` 语义；空池 cause 为 `ErrNoInstances`；`KindCanceled` 与单次尝试超时区分。
6. 耗尽尝试后，默认 fallback 处理 discovery/空池（`KindDiscovery`）、传输/读取失败（`KindTransport`）、单次尝试超时和最终 5xx（`KindStatus` 且 ≥500）。
7. 默认 fallback 不处理 4xx（含 429）、`KindDecode`、`KindInvalidCall`、`KindCanceled`。
8. 自定义 `WithAttemptPolicies` 可整体替换默认决策（启用/排除 4xx/429/解码类别、改变顺序）；`WithXxxAvailability` 可替换生成层默认可用性判断；不丢失原始原因。
9. 静态 fallback 和 fallback 工厂对生成服务方法保持类型安全；工厂收到正确 `*Failure`（与 `*Response`）；fallback 失败时能同时检查两类错误（`errors.Join`）。
10. 请求体重放和重试次数遵守配置上限；直连模式只重试相同 URL；冷却语义（只传输错误、降权不禁止、全冷却尝试、5xx 不冷却、可配时长）成立。
11. 并发调用时策略与冷却状态安全，单次请求的可变状态不共享。

## 留给实现阶段的设计细节

- `TargetMode` 常量命名与 `AttemptState` 的字段取舍（保持小而完整）。
- 默认 `[]AttemptPolicy` 切片字面量（文档中给出，供调用方复制修改）。
- `Failure` 的 `Error()`/`LogValue()` 格式化细节与脱敏规则（内部）。
- 生成模板对无返回值方法（仅返回 error 的 route）的 fallback 分派形式。
- 摘录上限（512B）与脱敏规则的内部常量位置。
