# jinx Target 与故障策略实施计划（评审修订版）

> **供 Agent 执行：** 必须按任务逐项执行，并使用 `superpowers:subagent-driven-development`（推荐）或 `superpowers:executing-plans`。步骤使用 `- [ ]` 核对框。
>
> **审核状态：** 用户已审阅并明确指示执行。

**目标：** 将仅支持服务发现的草稿客户端重构为显式服务/直连 target，提供 6 类有类型故障、函数类型尝试策略、本地冷却、公开 Transport 接缝，并支持 goctl 生成强类型 fallback（静态与工厂）。

**架构：** Core `Client` 持有经过校验的 `Target`，解析服务发现实例或单一直连 endpoint，返回结构化 `Failure{Kind, StatusCode, Err}`。尝试策略为函数类型切片（整体替换默认）；冷却为跨请求本地状态。core 只提供 `DefaultAvailability` 谓词；有类型 fallback 动作全部在生成层。

**技术栈：** Go 1.25、`net/http`、现有 resty transport（内部）、goctl 生成器及 golden/build 测试。

**设计方案：** `docs/superpowers/specs/2026-11-15-jinx-target-and-failure-strategy-design.md`

## 全局约束

- Go 1.25 workspace 三模块；core 不得依赖 go-zero 或 Eureka；resty 只在 core 内部且不得进公开签名或错误解包链。
- **Modern Go**：编辑任何 .go 文件前运行 `sh "C:/Users/Archer/.claude/plugins/cache/goland-claude-marketplace/modern-go-guidelines/1.1.1/skills/use-modern-go/scripts/run-tool.sh" list --go-version 1.25`，遵守返回的全部适用指南（`t.Context()`、`slices_*`/`maps_*`、`any`、`errors.Is`/`errors.Join`、`atomic_types`、`sync_once_func`、`cmp.Or`、`min`/`max`、`clear` 等）。
- 不保留当前草稿公开 API 的兼容性；公开面 = spec 最小面，一个不多（评审裁决）。
- `Target.URL` 非空时必须跳过 discovery 和 load balancer，失败后不得切换到 discovery。
- 默认仅在尝试策略耗尽后，对可用性故障执行 fallback；fallback 失败用 `errors.Join`。
- 错误必须保留原因和响应上下文；错误字符串/日志不得暴露 URL userinfo、query 值或未限长响应体（摘录上限 512B 内部常量）；`Failure.Unwrap` 链不得含 resty 类型。
- 不新增依赖。
- 每个 Client 的策略配置不可变；单次请求的执行状态不得跨并发请求共享；冷却表并发安全（`-race` 验证）。
- 测试：TDD（先写失败测试）+ 表驱动 + `-race`；live 集成测试环境变量门控（`JINX_EUREKA_URL`），CI 不跑。
- 提交：Conventional Commits（`feat:`/`docs:`），提交信息末尾附 `Co-Authored-By: Claude Code <noreply@anthropic.com>`。
- **文档仅中文**（用户指令，禁止英文文档）：README.zh-CN.md，不新建英文 README.md。

## 重点审查项

- target 不完整或格式错误时，必须在 discovery/transport 前失败（普通 error，非 Failure）；在任务 2 用构造校验测试固定。
- 直连 target 前缀、URL 基路径、route path、path 参数和 query 必须正确组合且保留转义；缺 path 参数在发起网络请求前报错；在任务 2 用 URL 断言固定。
- 单实例 5xx 不被冷却黑洞：冷却=降权不是禁用、全冷却照样尝试、只有传输错误进冷却；在任务 3 用冷却测试固定。
- 4xx 响应体完整可用（Response 非 nil、Body 全量、error 只带摘录）且 4xx 绝不转移；在任务 3 固定。
- 空池与 discovery 出错可区分：前者 cause 为 `ErrNoInstances`；在任务 3 用 `errors.Is` 固定。
- 调用方取消必须停止尝试和默认 fallback，且与单次尝试超时区分；在任务 3 用不同 context 测试固定。
- fallback 失败必须能与主故障分别检查；在任务 5 用 `errors.Is`/`errors.As` 断言固定。
- 默认不处理 4xx/429/解码/配置/取消错误；自定义 `WithAttemptPolicies` 整体替换、`WithXxxAvailability` 替换生成层判断；在任务 3、4、5 固定。
- 并发调用策略与冷却状态安全；在任务 3 用 `-race` 并发测试固定。

---

### Task 1: 建立结构化故障类型（6 类）

**文件：**
- 新建：`failure.go`
- 新建：`failure_test.go`
- 修改：`errors.go`、`errors_test.go`（删除 `TransportError` 定义移至任务 3 执行迁移时处理；本任务保留 `ErrNoInstances` 不动）

**接口：**
- 提供：

```go
type Kind uint8

const (
    KindInvalidCall Kind = iota // 请求构造/编码、实例选择、策略缺陷
    KindDiscovery               // 注册中心失败或空池；空池 cause = ErrNoInstances
    KindTransport               // 连接/DNS/TLS、单次尝试超时、响应读取失败
    KindStatus                  // 非 2xx；StatusCode 区分 4xx/5xx/429
    KindDecode                  // 2xx 解码失败
    KindCanceled                // 调用方取消/整体 deadline
)

type Failure struct {
    Kind       Kind
    StatusCode int
    Err        error
}
// Error() / Unwrap() / LogValue()
```

- `Failure` 只导出三个字段。可观测信息（客户端名、target 模式、尝试号/上限、实例 ID、脱敏 endpoint、摘录）不导出，由内部格式化器在 `Error()`/`LogValue()` 统一脱敏输出（无 URL userinfo、无 query 值；摘录上限 512B 为内部常量）。
- 不设 `PolicyError`/`FallbackError` 类型（评审裁决：fallback 失败走 `errors.Join`，策略缺陷走 `KindInvalidCall`）。

- [ ] **步骤 1：先编写失败测试**

新增 `TestFailureUnwrapsCause`、`TestFailureErrorSanitizesEndpointAndQuery`、`TestFailureLogValueCarriesObservability`、`TestFailureKindsExist`。断言 `errors.Is`/`errors.As` 语义；`Error()` 与 `LogValue()` 输出中不含 URL userinfo/query 值、不含未限长响应体；六类 Kind 常量存在且互异。

- [ ] **步骤 2：运行测试，确认测试先失败**

在 core 模块根目录运行：

```sh
go test ./... -run 'TestFailure' -count=1
```

预期：因 `Failure`/`Kind` 尚不存在而失败。

- [ ] **步骤 3：在 `failure.go` 实现故障类型**

实现 6 类 Kind、三字段 Failure、`Error()`/`Unwrap()`/`LogValue()`（内部格式化器脱敏 + 512B 摘录常量）。

- [ ] **步骤 4：重新运行聚焦测试**

运行：

```sh
go test -race ./... -run 'TestFailure' -count=1
```

预期：通过。

- [ ] **步骤 5：提交**

```sh
git add failure.go failure_test.go
git commit -m "feat: 添加六类结构化故障类型

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

### Task 2: 增加显式服务与直连 Target

**文件：**
- 新建：`target.go`、`target_test.go`
- 修改：`client.go`、`client_test.go`、`request.go`、`request_test.go`、`url.go`、`url_test.go`
- 修改：`goctl/template.go`、`goctl/testdata/client.golden`、`goctl/gen_test.go`，使生成代码继续兼容新的请求 API

**接口：**
- 使用任务 1 提供的 `Failure`、`KindInvalidCall`。
- 提供：

```go
type TargetMode uint8
const ( TargetService TargetMode = iota; TargetDirect )

type Target struct { Name, URL, Path string }
func (t Target) Mode() TargetMode
func (t Target) Validate() error // 普通 error，不产生 Failure

func New(t Target, opts ...Option) (*Client, error) // 校验失败返回普通 error
func WithDiscovery(DiscoveryClient) Option          // URL == "" 时必填；URL != "" 时忽略
func (*Client) NewRequest(method, path string) *Request
```

- `Target.Name` 始终必填。`URL` 为空 = 服务发现模式，discovery 缺失 → 构造错误（不是模式推断）。`URL` 非空 = 直连模式：必须带 host 的绝对 `http`/`https` URL，不得含 query/fragment；discovery 可为 nil。
- `Target.Path` 与 URL 基路径不得含 query/fragment；query 值由 `Request` 承载。
- 直连模式按 URL 基路径、`Target.Path`、route path 顺序合并路径，再处理 path 参数，无双斜杠；query 参数单独保留。服务发现 endpoint 仍由实例 scheme/host/port 与 target/route 路径组成。

- [ ] **步骤 1：先编写失败的 Target 和解析测试**

新增 `TestNew_RequiresTargetName`、`TestNew_ServiceTargetRequiresDiscovery`、`TestNew_DirectTargetDoesNotRequireDiscovery`、`TestDirectTarget_NeverCallsDiscoveryOrLoadBalancer`、`TestDirectTarget_RejectsMalformedAndUnsupportedURLs`、`TestDirectTarget_JoinsBasePrefixRoutePathAndEscapedParams`、`TestRequest_MissingPathParamFailsBeforeNetwork`（计数 fake transport 断言 0 次调用）。迁移仓库内所有 `New`/`NewRequest` 调用点，使用 `Target{Name: "orders"}` 并改用 `NewRequest(method, path)`。

- [ ] **步骤 2：运行 Target 测试，确认测试先失败**

在 core 模块根目录运行：

```sh
go test ./... -run 'TestNew_|TestDirectTarget|TestRequest_MissingPathParam' -count=1
```

预期：因 Target API 缺失或当前仍为 discovery-only 行为而失败。

- [ ] **步骤 3：实现 `Target` 并迁移 core 构造逻辑**

增加 target 校验与显式模式选择；构造错误为普通 error。更新 `Client` 与 `Request` 使用已配置 target；不得通过 discovery 是否为 nil 推断直连模式。headers、query、body、result、transport 的共享行为不变。

- [ ] **步骤 4：更新生成客户端 API 并运行聚焦测试**

更新 goctl 模板和 golden，使生成方法调用 `NewRequest(method, path)`；生成客户端构造函数本阶段仍接收已配置的 `*jinx.Client`。在 core 模块根目录运行 `go test ./... -count=1`，再在 `goctl` 目录运行同一命令。

预期：均通过；直连请求不调用 discovery 或 load balancer。

- [ ] **步骤 5：提交**

```sh
git add target.go target_test.go client.go client_test.go request.go request_test.go url.go url_test.go goctl/template.go goctl/testdata/client.golden goctl/gen_test.go
git commit -m "feat: 增加显式请求 Target

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

### Task 3: 故障分类、尝试策略与冷却

**文件：**
- 新建：`strategy.go`、`strategy_test.go`
- 修改：`client.go`、`request.go`、`errors.go`、`errors_test.go`、`request_test.go`、`client_test.go`、`failover.go`（保留冷却语义）、`transport.go`（提升为公开 Transport）

**接口：**
- 使用任务 1、2 的 `Target`、`Failure`、`Kind` 与请求 API。
- 提供：

```go
type AttemptAction uint8
const (
    AttemptNext AttemptAction = iota
    AttemptRetrySameEndpoint
    AttemptFailover // 直连模式无效，等同 Next
    AttemptStop
)

type AttemptState struct {
    Attempt, AttemptLimit, UntriedEndpoints int
    TargetMode                             TargetMode
    Replayable                             bool
}

type AttemptPolicy func(ctx context.Context, f *Failure, s AttemptState) (AttemptAction, error)

func WithAttemptPolicies([]AttemptPolicy) Option // 整体替换默认策略链
func WithCooldown(d time.Duration) Option        // 默认 10s；<0 关闭
func WithTransport(Transport) Option             // 覆盖默认 resty transport

type Transport func(ctx context.Context, req *http.Request) (*http.Response, error)
```

- core 按切片顺序执行策略；第一个非 `AttemptNext` 生效；全部 `AttemptNext` = 停止。无 per-request 策略层（评审裁决 1）。
- 策略自身出错：包装为 `KindInvalidCall` 的 `Failure`，原 `Failure` 为 cause。
- 默认策略仅对传输、响应读取和 5xx 故障重试同一直连 endpoint 或切换到未尝试过的服务实例；4xx、`KindInvalidCall`、`KindDecode`、`KindCanceled` 默认停止。core 强制执行尝试上限、请求体可重放与取消约束（策略无法突破）。
- **冷却语义**（恢复旧设计，评审裁决 4）：只有 `KindTransport` 把实例放入冷却；5xx 永不冷却；候选过滤先剔冷却实例，若全部冷却则全部尝试（降权不禁止）；默认 10s、`WithCooldown` 可配、`<0` 关闭；冷却表并发安全。
- `Request.Do` 每个执行错误返回 `*Failure`：6 类分类；空池 `KindDiscovery` 且 cause = `ErrNoInstances`；单次尝试超时（`KindTransport`）与调用方取消（`KindCanceled`）区分；resty 错误链消毒（unwrap 链无 resty 类型）。
- 迁移时删除 `TransportError` 公开类型（错误分类由 `KindTransport` 接管）；保留 `ErrNoInstances`。

- [ ] **步骤 1：先编写失败的尝试、分类与冷却测试**

新增 `TestAttemptPolicy_NextAndFirstDecisionWins`、`TestAttemptPolicy_ErrorPreservesFailure`、`TestDirectTarget_RetriesSameEndpointWithinBudget`、`TestServiceTarget_FailsOverOnlyToUntriedInstances`、`TestDo_ClassifiesBoundariesAndUnwrapsCause`（6 类 + `ErrNoInstances` cause + resty 消毒断言）、`TestDo_DistinguishesCallerCancellationFromAttemptTimeout`、`TestDo_ReplaysJSONBodyAcrossAttempts`、`TestDo_ConcurrentAttemptStateIsRequestLocal`（`-race`）、`TestTransportOption_OverridesDefault`。保留/恢复冷却测试：`TestFailover_TransportErrorCooldown`、`TestFailover_AllCooldownStillAttempts`、`TestFailover_NoCooldownOn5xx`。断言 4xx 不转移且 Response 完整、最终 5xx 保留状态、取消后不再调用 transport。

- [ ] **步骤 2：运行聚焦测试，确认测试先失败**

在 core 模块根目录运行：

```sh
go test ./... -run 'TestAttemptPolicy|TestDirectTarget_Retries|TestServiceTarget_FailsOver|TestDo_|TestTransportOption|TestFailover_' -count=1
```

预期：因尝试策略与有类型请求错误尚未实现而失败。

- [ ] **步骤 3：实现尝试策略、冷却与有类型执行流程**

将 endpoint 选择、重试、故障转移与冷却迁移到新契约后；每个故障边界产生 6 类之一；保留受限响应上下文与原始原因；强制尝试上限、取消、重放约束；所有可变计数/候选集合局限于单次 `Do`。`transport.go` 的 resty 实现保持内部默认，公开 `Transport` 类型 + `WithTransport`。

- [ ] **步骤 4：运行 core 测试**

在 core 模块根目录运行 `go test -race ./... -count=1`。

预期：通过，包括迁移到有类型 `Failure` 的负载均衡、冷却与请求测试。

- [ ] **步骤 5：提交**

```sh
git add strategy.go strategy_test.go client.go client_test.go request.go request_test.go errors.go errors_test.go failover.go transport.go
git commit -m "feat: 分类故障、组合尝试策略并恢复冷却

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

### Task 4: 默认可用性分类

**文件：**
- 新建：`availability.go`、`availability_test.go`

**接口：**
- 使用任务 1、3 的 `Failure`、`Kind`。
- 提供：

```go
func DefaultAvailability(f *Failure) bool
```

- 真：`KindDiscovery`、`KindTransport`、`KindStatus` 且 `StatusCode >= 500`（重试/转移耗尽后的最终 5xx）。
- 假：`KindStatus` 且 `StatusCode < 500`（4xx 含 429）、`KindDecode`、`KindInvalidCall`、`KindCanceled`。
- core 不设 recovery 链、不设 classifier 选项（评审裁决 3；生成层用 `WithXxxAvailability` 谓词替换）。

- [ ] **步骤 1：先编写失败的分类测试**

新增 `TestDefaultAvailability_DefaultKindsAndStatus`（表驱动：上述真/假全集，含 429、499、500、503）。

- [ ] **步骤 2：运行测试，确认测试先失败**

在 core 模块根目录运行：

```sh
go test ./... -run 'TestDefaultAvailability' -count=1
```

预期：因 `DefaultAvailability` 尚未实现而失败。

- [ ] **步骤 3：实现 `availability.go`**

- [ ] **步骤 4：运行 core 测试**

在 core 模块根目录运行 `go test -race ./... -count=1`。

预期：通过。

- [ ] **步骤 5：提交**

```sh
git add availability.go availability_test.go
git commit -m "feat: 增加默认可用性分类

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

### Task 5: 生成类型安全的 Fallback 客户端

**文件：**
- 修改：`goctl/gen.go`、`goctl/template.go`、`goctl/gen_test.go`、`goctl/testdata/client.golden`
- 仅当独立 fixture 能明显改善 golden 可读性时新建：`goctl/testdata/fallback_client.golden`

**接口：**
- 使用 target、`Failure`、`DefaultAvailability`、`errors.Join`。
- 为生成的 `${Service}` 提供 `${Service}ClientConfig`：

```go
type ${Service}ClientConfig struct {
    Target         jinx.Target
    Discovery      jinx.DiscoveryClient                                     // 服务 target 必填
    ClientOptions  []jinx.Option
    Fallback       ${Service}                                               // 静态 fallback，实现生成接口
    FallbackFactory func(context.Context, *jinx.Failure, *jinx.Response) (${Service}, error)
    Availability   func(*jinx.Failure) bool                                 // 默认 jinx.DefaultAvailability
}
```

- 生成 `New${Service}Client(config ${Service}ClientConfig) (${Service}, error)`。静态 `Fallback` 与 `FallbackFactory` 互斥：同时配置 = 构造错误（报错串写明冲突）。
- 插件同时生成 `types/types.go`（复用 goctl `gogen.BuildTypes`），调用方**不需要先运行 `goctl api go`**（第三方服务场景只要声明如何调用，避免生成 server 侧 routes/handler/logic）。types 为导出子包（非 internal）：契约模块被其他模块 import 时消费方可导入 `<module>/types`。
- 生成方法接收单个请求结构体，字段按 goctl tag 分区（path/header/form/json），form 在 GET/DELETE/HEAD/OPTIONS 走 query、其余走 form body；json 与 form 混用生成期报错。
- 每个生成方法单一分派路径（模板不得 switch `Failure.Kind`）：

```go
resp, err := req.Do(ctx)
var f *jinx.Failure
if err != nil && errors.As(err, &f) && availability(f) { /* 调 fallback 实现对应方法 */ }
```

- `FallbackFactory` 接收 `*Failure` 与 `*Response`（传输失败时 `Response` 为 nil）。fallback 实现不被递归包装进主客户端，按原参数直接调用其方法。fallback 方法自身失败：`errors.Join(原始 Failure, fmt.Errorf("jinx: fallback: %w", fbErr))`。
- 不设 `RecoveryPolicies`/`ReplaceRecoveryPolicies`/context 附加策略（评审裁决 1、3）。

- [ ] **步骤 1：先编写失败的生成客户端测试**

增加生成器断言和可编译的生成代码 fixture，证明：客户端配置支持直连或服务 target；静态 fallback 在 discovery、空池、传输、单次尝试超时和最终 5xx 耗尽后运行；fallback 工厂收到同一 `*Failure`（与 `*Response`）；4xx、解码失败和调用方取消不触发默认 fallback；自定义 `Availability` 谓词可恢复 429 和解码错误；fallback 方法失败时能分别检查原始故障与 fallback 错误（`errors.Is`/`errors.As`）；静态 fallback 与工厂同配构造报错。更新 golden。

- [ ] **步骤 2：运行 goctl 测试，确认测试先失败**

在 `goctl` 目录运行：

```sh
go test ./... -run 'TestGenerate|TestGeneratedClient' -count=1
```

预期：因生成客户端配置与强类型 fallback 缺失而失败。

- [ ] **步骤 3：实现生成客户端的强类型 fallback**

更新模板：从生成配置构造 core 客户端（`Target` + `WithDiscovery` + `ClientOptions`）、保留服务方法签名、实现共用的主实现/fallback 分派路径。通过生成代码编译测试验证静态 fallback 与工厂的类型安全。

- [ ] **步骤 4：运行 goctl 测试、构建和 vet**

在 `goctl` 目录运行：

```sh
go test ./... -count=1
go build ./...
go vet ./...
```

预期：全部通过；生成 fixture 可编译，且其中 fallback 测试通过。

- [ ] **步骤 5：提交**

```sh
git add goctl/gen.go goctl/template.go goctl/gen_test.go goctl/testdata/client.golden goctl/testdata/fallback_client.golden
git commit -m "feat: 生成强类型 fallback 客户端

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

### Task 6: 更新文档并验证 Workspace 集成

**文件：**
- 修改：`README.zh-CN.md`、`goctl/README.zh-CN.md`（文档仅中文，不新建英文 README）
- 验证：core、`eureka`、`goctl` 三个模块

**接口：**
- 使用任务 1–5 产出的公开 API。
- 中文示例覆盖：服务发现、URL 直连、自定义尝试策略（函数类型 + 整体替换）、冷却配置、公开 Transport、默认可用性 fallback、静态 fallback、fallback 工厂与自定义 `Availability` 谓词。

- [ ] **步骤 1：更新文档示例**

替换仅支持服务发现的构造和请求示例。说明两种模式下 `Target.Name` 都必填；直连跳过 discovery/load balancing；4xx/429 和解码错误不触发默认 fallback；`WithAttemptPolicies` 整体替换默认链；冷却语义（只传输错误、降权不禁止、全冷却尝试、5xx 不冷却）；静态 fallback 与工厂互斥。

- [ ] **步骤 2：检查 spec 覆盖**

逐条对照 spec 验证要求 1–11 与任务 2–5 的测试。若有遗漏，将测试补到对应行为所属任务，特别检查：路径组合/转义、无效 target 不发起网络请求、直连重试上限、错误解包、取消、默认 fallback 排除项、策略替换、冷却、请求体重放与并发调用。

- [ ] **步骤 3：运行各模块测试与静态检查**

在每个模块根目录依次运行：

```sh
go test -race ./... -count=1
go vet ./...
go build ./...
```

依次检查 core 根目录、`eureka` 和 `goctl`。预期：三个模块的所有命令均通过。

- [ ] **步骤 4：提交**

```sh
git add README.zh-CN.md goctl/README.zh-CN.md
git commit -m "docs: 说明 Target 与故障策略

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```
