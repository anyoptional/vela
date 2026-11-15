# vela 负载均衡 HTTP 客户端 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 vela：基于 eureka-go 服务发现的负载均衡 HTTP 客户端库（core + eureka 适配器 + goctl 插件三模块），公开 API 按已审计的最小面。

**Architecture:** Go workspace 三模块。core（`github.com/anyoptional/vela`，根目录）持有 DiscoveryClient/LoadBalancer 接口、失败转移循环、冷却、resty 传输边界；eureka 适配器（`eureka/`，模块 `github.com/anyoptional/vela-eureka`）实现 DiscoveryClient；goctl 插件（`goctl/`，模块 `github.com/anyoptional/vela-goctl`）生成调 core 的 typed interface + 实现。

**Tech Stack:** Go 1.25、`resty.dev/v3` v3.0.0-rc.4（仅 core 内部）、github.com/anyoptional/eureka-go v1.0.0（仅适配器）、github.com/zeromicro/go-zero tools/goctl（仅插件）、slog。

**Spec:** `docs/superpowers/specs/2026-11-15-vela-loadbalanced-http-client-design.md`

## Global Constraints

- Go 1.25；仓库根 `go.work`（use: `.`、`./eureka`、`./goctl`），三个独立 go.mod。
- **Modern Go**：编辑任何 .go 文件前运行 `sh "C:/Users/Archer/.claude/plugins/cache/goland-claude-marketplace/modern-go-guidelines/1.1.1/skills/use-modern-go/scripts/run-tool.sh" list --go-version 1.25`，遵守返回的全部适用指南（已知适用：`wg.Go`（如 spawn goroutine）、`t.Context()`、`omitzero`、`slices_*`/`maps_*`、`any`、`errors.Is`/`errors.Join`、`atomic_types`、`sync_once_func`、`cmp.Or`、`min`/`max`、`clear`、`range_over_int`）。
- 依赖隔离（反目标）：resty 类型绝不进公开签名；eureka-go 只在 eureka 模块；go-zero/goctl 只在插件模块。core 公开面 = spec 审计后最小面（见下），一个都不多。
- 语义契约：`Do` 收到任何 HTTP 响应（含 4xx/5xx）都返回非 nil Response，非 2xx 时 error 非 nil（状态码 + 有界 body 摘录）；4xx 不转移；5xx 转移不冷却；只有传输错误进冷却；全部候选冷却时照样尝试；同请求不重试同实例两次。
- 测试：表驱动 + `-race`；live 集成测试环境变量门控（`vela_EUREKA_URL`，缺省跳过），对照 eureka-go `live_integration_test.go` 模式。
- 提交：Conventional Commits（`feat:`/`test:`/`docs:`），提交信息末尾附 `Co-Authored-By: Claude Code <noreply@anthropic.com>`。Task 0 完成 `git init` 后每个任务以提交收尾。
- 文档：每个模块 README.md + README.zh-CN.md（对照 eureka-go 双语惯例）。

## Review Focus

1. 单实例服务返回一个 5xx 后，紧接着的下一个请求不能被冷却黑洞——冷却只是降权，必须照样尝试。→ Task 4 测试 `TestFailover_AllCooldownStillAttempts`。
2. 4xx 响应体必须完整可用：Response 非 nil、StatusCode 对、Body 全量、error 只带摘录；且 4xx 绝不触发换实例。→ Task 4 测试 `TestFailover_NoFailoverOn4xx`。
3. path 参数缺值（`:id` 在 path 里但没 `SetPathParam`）必须在任何网络请求前报错；`@server` prefix 与 route path 拼接不得产生双斜杠或丢斜杠。→ Task 3 测试 `TestRequest_MissingPathParam`；Task 4 测试 `TestClient_PrefixJoin`。
4. discovery 返回空列表 vs discovery 本身出错，必须是两种可区分结果：前者 `errors.Is(err, ErrNoInstances)`，后者是 wrapped 发现错误。→ Task 4 测试 `TestDo_DiscoveryErrors`。
5. 并发调用共享 Client：RR 计数器与冷却 map 无数据竞争（`-race` 下跑）。→ Task 4 测试 `TestClient_ConcurrentRR`。

---

### Task 0: 脚手架（workspace + 三模块 + git）

**Files:**
- Create: `.gitignore`、`go.work`、`go.mod`（根）、`eureka/go.mod`、`goctl/go.mod`

**Interfaces:**
- Produces: 三个模块路径——`github.com/anyoptional/vela`（根，package `vela`）、`github.com/anyoptional/vela-eureka`（`eureka/`，package `eurekaadapter`）、`github.com/anyoptional/vela-goctl`（`goctl/`，package `main`，二进制名 `goctl-vela`）

- [ ] **Step 1: git init + .gitignore**

```bash
cd D:/Codes/WorkspaceGo/vela && git init
```

`.gitignore` 内容：`bin/`、`*.exe`、`*.test`、`*.out`。

- [ ] **Step 2: 根 go.mod**

`module github.com/anyoptional/vela`，`go 1.25`，`require resty.dev/v3 v3.0.0-rc.4`（首次 import 发生在 Task 5，届时 `go mod tidy` 固化；本任务只写 require，不在本任务跑 tidy）。

- [ ] **Step 3: eureka/go.mod**

`module github.com/anyoptional/vela-eureka`，`go 1.25`，`require github.com/anyoptional/eureka-go v1.0.0` + `github.com/anyoptional/vela v0.0.0`（go.work 自动解析，不加 replace）。

- [ ] **Step 4: goctl/go.mod**

`module github.com/anyoptional/vela-goctl`，`go 1.25`，`require github.com/zeromicro/go-zero v1.10.1` + `github.com/anyoptional/vela v0.0.0`。

- [ ] **Step 5: go.work**

```bash
go work init . ./eureka ./goctl
```

- [ ] **Step 6: 验证空模块可编译**

Run: `go build ./...`（根）、`cd eureka && go build ./...`、`cd goctl && go build ./...`
Expected: 全部 PASS（空包）。

- [ ] **Step 7: 提交**

```bash
git add -A && git commit -m "chore: scaffold go workspace with three modules

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

### Task 1: core 类型与接口 + 内置 round-robin

**Files:**
- Create: `instance.go`、`discovery.go`、`loadbalancer.go`、`errors.go`（均 package `vela`，根目录）
- Test: `loadbalancer_test.go`

**Interfaces:**
- Produces（公开面，后续任务依赖，与 spec 最小面一致）:

```go
type Instance struct {
    ID        string
    ServiceID string
    Host      string
    Port      int
    Secure    bool
    Metadata  map[string]string
}

type DiscoveryClient interface {
    Description() string
    Instances(ctx context.Context, serviceID string) ([]Instance, error)
    Services(ctx context.Context) ([]string, error)
}

type LoadBalancer interface {
    Choose(ctx context.Context, serviceID string, candidates []Instance) (Instance, error)
}

var ErrNoInstances = errors.New("vela: no instances available")

type TransportError struct{ Err error }
// Error(): "vela: transport: " + Err.Error()；Unwrap() 返回 Err
```

- Produces（内部 seam）：`type roundRobin struct { mu sync.Mutex; next map[string]int }`、`func newRoundRobin() *roundRobin`、`func (r *roundRobin) Choose(ctx context.Context, serviceID string, candidates []Instance) (Instance, error)`

- [ ] **Step 1: 写失败测试 `TestRoundRobin_Sequential` / `TestRoundRobin_WrapAround` / `TestRoundRobin_PerServiceCounters` / `TestRoundRobin_Empty`**

断言：3 候选依次返回 c0,c1,c2,c0；不同 serviceID 计数互不影响；空候选返回 `ErrNoInstances`（用 `errors.Is`）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./... -run TestRoundRobin -v`
Expected: FAIL（编译错误：类型不存在）

- [ ] **Step 3: 实现四个文件的类型与 roundRobin**

roundRobin 每次 Choose 取 `next[serviceID] % len(candidates)` 后自增；空 candidates 返回 `ErrNoInstances`。instance.go/discovery.go 按 Produces 签名；errors.go 含 `ErrNoInstances` 与 `TransportError`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 5: 提交** `feat: add core types, interfaces and round-robin loader`（附 Co-Authored-By）

### Task 2: Client 构造与配置

**Files:**
- Create: `client.go`（`Client`、`config`、`New`、Option 函数、`NewRequest` 骨架）
- Test: `client_test.go`

**Interfaces:**
- Produces:

```go
func New(discovery DiscoveryClient, opts ...Option) *Client
type Option func(*config) // config unexported
func WithLoadBalancer(lb LoadBalancer) Option
func WithTimeout(perAttempt time.Duration) Option
func WithMaxAttempts(n int) Option
func WithCooldown(d time.Duration) Option
func WithLogger(l *slog.Logger) Option
func WithHTTPClient(c *http.Client) Option
func (c *Client) NewRequest(serviceID, method, path string) *Request // Request 在 Task 3
```

- 默认值：lb = 内部 roundRobin；timeout 5s/尝试；maxAttempts 3；cooldown 10s；logger = `slog.Default()`；httpClient = `&http.Client{}`。选项值 ≤0 回退默认（cooldown 例外：<0 视为 0 = 关闭冷却）。

- [ ] **Step 1: 写失败测试 `TestNew_Defaults` / `TestNew_Options` / `TestNew_InvalidOptionsFallback`**

断言：缺省构造后内部字段为上述默认；逐项 option 覆盖生效；`WithMaxAttempts(0)`、`WithTimeout(-1s)` 回退默认，`WithCooldown(-1s)` 得 0。用同包测试访问 unexported 字段。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./... -run 'TestNew_' -v`
Expected: FAIL

- [ ] **Step 3: 实现 `client.go`**

`config` 结构体含 lb、perAttemptTimeout、maxAttempts、cooldown、logger、httpClient；`Client` 含 discovery、lb、cfg、httpClient、logger、`cool cooldownTracker`（Task 4 定义字段，本任务先留注释占位或在 Task 4 补字段——本任务只实现构造）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 5: 提交** `feat: add client construction and options`

### Task 3: Request 构建器 + URL 构建 + path 参数展开

**Files:**
- Create: `request.go`（`Request` + 五个 Set 方法 + `Do` 骨架签名）、`url.go`（内部 `buildURL`）
- Test: `request_test.go`、`url_test.go`

**Interfaces:**
- Consumes: Task 2 的 `NewRequest`
- Produces:

```go
type Request struct{ /* unexported: serviceID, method, path, pathParams, query, headers, body any, result any, c *Client */ }
func (r *Request) SetPathParam(name, value string) *Request
func (r *Request) SetQueryParam(name, value string) *Request
func (r *Request) SetHeader(name, value string) *Request
func (r *Request) SetBody(v any) *Request
func (r *Request) SetResult(v any) *Request
func (r *Request) Do(ctx context.Context) (*Response, error) // 循环逻辑 Task 4
type Response struct {
    StatusCode int
    Body       []byte
}
```

- Produces（内部 seam）：`func buildURL(ins Instance, path string, pathParams map[string]string) (string, error)` — scheme `https` iff `ins.Secure`；展开 `:name`；缺失参数返回 `fmt.Errorf("vela: missing path param %q", name)`；path 空 → `/`。

- [ ] **Step 1: 写失败测试 `TestBuildURL`（表驱动）/ `TestRequest_Setters`**

buildURL 用例：plain `http://h:8080/a`；secure `https://h:443/a`；`:id` 展开；缺参数报错且 error 串含 `missing path param "id"`；`"/v1"`+`"/orders/:id"` 拼接无双斜杠。Setters 用例：链式返回同指针；SetBody/SetResult 值留存。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./... -run 'TestBuildURL|TestRequest_Setters' -v`
Expected: FAIL

- [ ] **Step 3: 实现 `request.go` + `url.go`**

`url.go` 里 `joinPath(prefix, path)` 用 `strings.TrimRight(prefix,"/") + "/" + strings.TrimLeft(path,"/")`；`buildURL` 用 `net.JoinHostPort(ins.Host, strconv.Itoa(ins.Port))` 拼 host。SetBody 只存值，JSON 序列化放 Do（Task 4）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 5: 提交** `feat: add request builder and url construction`

### Task 4: Do 失败转移循环（fake transport 接缝）

**Files:**
- Create: `failover.go`（`cooldownTracker`、tried-set、循环主体）
- Modify: `client.go`（补 `cool cooldownTracker` 字段、`transport` 接缝、`Do` 调用）、`request.go`（Do 实现）
- Test: `client_test.go`（fake transport 注入）

**Interfaces:**
- Produces（内部 seam）：`type transport func(ctx context.Context, req *http.Request) (*http.Response, error)`；`Client.transport` 字段默认由 Task 5 的 resty 实现填充（本任务测试用同包注入 fake）。
- Produces（内部 seam）：`type cooldownTracker struct { mu sync.Mutex; until map[insKey]time.Time; now func() time.Time }`，`insKey = struct{ secure bool; host string; port int }`；方法 `mark(ins Instance)`、`isCooling(ins Instance) bool`、`len()`。
- Do 算法（实现按此，无自由发挥）：
  1. `Instances(ctx, serviceID)`；err → `fmt.Errorf("vela: discover %q: %w", serviceID, err)`；空 → `ErrNoInstances`。
  2. 循环 ≤ maxAttempts：候选过滤（去 tried；若有非冷却实例只用非冷却，否则用全部冷却实例）；过滤后空 → 跳出。
  3. `lb.Choose` → `buildURL` → 组 `*http.Request`（body 用 `json.Marshal`，失败即返回 marshal 错误，不触网）→ `transport(ctx, req)`；记录 tried。
  4. 2xx → `SetResult` 解码（`json.Unmarshal` 到 result，解码失败返回 wrapped 错误）并返回 Response；4xx → 返回 Response + `fmt.Errorf("vela: unexpected status %d: %s", code, 摘录)`（摘录 = body 前 512B）；5xx → 继续（不冷却）；transport err → `cooldownTracker.mark` + 继续。
  5. 循环耗尽：最后一次是 transport err → `&TransportError{Err: err}` 包 attempts 计数入 message；最后一次是 5xx → 5xx 错误；否则 `ErrNoInstances`。

- [ ] **Step 1: 写失败测试（fake transport：map 按 host 路由到预设响应/错误，记录调用顺序）**

- `TestDo_FirstAttemptSuccess`：2xx、SetResult 解码出值、Response.Body 完整
- `TestFailover_NextInstanceOn5xx`：实例 A 500、B 200 → 结果 200 且第一次尝试 host=A、第二次 host=B
- `TestFailover_NoFailoverOn4xx`（Review Focus 2）：404 → Response 非 nil、StatusCode 404、Body 全量、error 非 nil、transport 只被调 1 次
- `TestFailover_TransportErrorCooldown`：A 传输错 → 同一 Client 第二次请求跳过 A 选 B；注入 `now` 假时钟验证冷却到期后 A 恢复可选
- `TestFailover_AllCooldownStillAttempts`（Review Focus 1）：单实例冷却中 → 照样尝试
- `TestDo_DiscoveryErrors`（Review Focus 4）：空列表 → `errors.Is(err, ErrNoInstances)`；返回 error → wrapped、`!errors.Is(err, ErrNoInstances)` 且串含 `discover`
- `TestDo_NoRepeatSameInstance`：单实例持续 500 → 尝试次数 = 1（tried 过滤后无候选）
- `TestClient_PrefixJoin`（Review Focus 3）：path `"/v1"+"/orders/:id"` 实际请求 URL 无双斜杠
- `TestDo_MissingPathParamNoNetwork`（Review Focus 3）：缺 `:id` → 返回错误且计数 transport 调用 = 0
- `TestClient_ConcurrentRR`（Review Focus 5）：2 fake 实例、100 goroutine × 10 请求，`-race` 无竞争

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./... -run 'TestDo_|TestFailover_|TestClient_' -v`
Expected: FAIL（Do 未实现/骨架返回零值）

- [ ] **Step 3: 实现 `failover.go` + Do**

按上面算法。body 序列化只做一次（循环前）。`prune`：mark 时顺带清理过期条目（用 `now()`）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 5: 提交** `feat: add failover loop with cooldown`

### Task 5: resty 传输边界适配器

**Files:**
- Create: `transport.go`（`newRestyTransport`）
- Modify: `client.go`（`New` 里默认 `transport = newRestyTransport(cfg, logger)`）
- Test: `transport_test.go`（httptest 真实 HTTP）

**Interfaces:**
- Consumes: Task 4 的 `transport` seam；Task 2 的 config
- Produces（内部）：`func newRestyTransport(cfg config, logger *slog.Logger) transport` — resty v3 客户端：重试全关（转移由 Task 4 循环做）、每尝试超时 = cfg.perAttemptTimeout、UA `vela`、header/query 透传；错误一律转成**不含 resty 类型的** error（`errors.New(清洗后的消息)`），再包 `TransportError`。

- [ ] **Step 1: 写失败测试 `TestRestyTransport_RoundTrip` / `TestRestyTransport_Timeout` / `TestRestyTransport_ErrorSanitized`**

httptest server 断言 method/path/header/query/body 原样到达；timeout server 睡眠超过 WithTimeout → error 且 `errors.As(err, &TransportError)` 为真；错误链 `Unwrap` 出的类型不是 resty 包类型（断言 `reflect.TypeOf(...).PkgPath() != "resty.dev/v3"` 链上每一环）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./... -run 'TestRestyTransport_' -v`
Expected: FAIL

- [ ] **Step 3: 实现 `transport.go`**

对照 resty v3 API（`resty.dev/v3`）：`resty.New()`、`SetBaseURL` 不用（URL 已拼好）、`SetTimeout`、`SetRetryCount(0)`；执行用 `client.R().SetBody().SetHeaders().SetQueryParams().Execute(req.Method, req.URL.String())`，读 `resp.RawBody()`（或等价 API，以 v3 实际为准）拷贝 body。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 5: 提交** `feat: add resty transport adapter`

### Task 6: eureka 适配器模块

**Files:**
- Create: `eureka/adapter.go`、`eureka/adapter_test.go`、`eureka/live_integration_test.go`、`eureka/README.md`、`eureka/README.zh-CN.md`

**Interfaces:**
- Consumes: Task 1 的 `vela.Instance` / `vela.DiscoveryClient`；eureka-go `Client.InstancesOf(name, eureka.WithStatus(eureka.UP))`、`Client.Apps()`、`Instance.Metadata.GetMap()`
- Produces:

```go
package eurekaadapter

func New(c *eureka.Client) vela.DiscoveryClient
// Description() = "eureka-go"
// Services(ctx) = keys of c.Apps()
// Instances(ctx, serviceID) = c.InstancesOf(serviceID, eureka.WithStatus(eureka.UP)) 映射后返回
```

- 映射规则（内部 `func mapInstance(in *eureka.Instance) (vela.Instance, bool)`）：`Host` = `IPAddr`，空则 `HostName`；`Secure` = `SecurePort.Enabled`；secure 走 `SecurePort.Number` 否则 `Port.Number`；`Port.Enabled=false` 且 `SecurePort.Enabled=false` → 返回 false（滤掉）；`ID` = `in.Id()`；`ServiceID` = serviceID 入参；`Metadata` = `GetMap()` 的值 `fmt.Sprint` 成 `map[string]string`。
- 日志桥接（内部 `func newSlogLogr(l *slog.Logger) logr.Logger`）：把 slog 包装成 logr，供 `eureka.Client.SetLogger` 使用（slog→logr 只映射 Info/Error 两档，键值对透传）。

- [ ] **Step 1: 写失败测试 `TestMapInstance`（表驱动）+ `TestSlogLogr_Smoke`**

用例：UP + IPAddr 优先；IPAddr 空用 HostName；SecurePort.Enabled=true 走 secure 端口；双端口 disabled → 滤掉；Metadata 值转 string。构造 `eureka.Instance` 字面量，不触网。Smoke：`newSlogLogr` 的 Info 经 slog handler 落盘、键值保留。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd eureka && go test ./... -run TestMapInstance -v`
Expected: FAIL

- [ ] **Step 3: 实现 `adapter.go`**

- [ ] **Step 4: 跑测试确认通过**

Run: `cd eureka && go test -race ./...`
Expected: PASS

- [ ] **Step 5: live 集成测试（env 门控）**

`live_integration_test.go`：`vela_EUREKA_URL` 未设 → `t.Skip`；已设 → 用 eureka-go 注册一个临时实例，断言 adapter `Instances` 能看到。Run: `vela_EUREKA_URL=... go test ./... -run TestLive -v`（本地无 eureka 则跳过，CI 不跑）。

- [ ] **Step 6: README 双语**（用法：`eurekaadapter.New(eurekaClient)` 喂给 `vela.New`）

- [ ] **Step 7: 提交** `feat: add eureka discovery adapter`

### Task 7: goctl 插件

**Files:**
- Create: `goctl/main.go`、`goctl/gen.go`（遍历 spec + 类型路径推导）、`goctl/template.go`（`text/template`）、`goctl/gen_test.go`、`goctl/testdata/*.api` + 对应 golden 文件、`goctl/README.md`、`goctl/README.zh-CN.md`

**Interfaces:**
- Consumes: goctl 插件协议 `github.com/zeromicro/go-zero/tools/goctl/plugin`（`plugin.NewPlugin()` → `Api *spec.ApiSpec`、`Dir`、`Style`）；命名转换 `github.com/zeromicro/go-zero/core/stringx`；core 模块 `vela.Client`/`vela.Request`
- Produces：`Dir/client.go`——interface 名 = `stringx.From(serviceName).ToCamel()`（service 块名）；方法名 = `@handler` 的 Camel；每个 route：

```go
// 例：@handler GetOrder  get /orders/:id (GetOrderReq) returns (GetOrderResp)
GetOrder(ctx context.Context, id string, req *types.GetOrderReq) (*types.GetOrderResp, error)
```

  - path 参数按出现顺序做位置参数（类型 = spec 里声明类型）；有 RequestType 才加 req 参数；GET/DELETE 无 body
  - **form/query 参数 → 直接报错** `"vela-goctl: unsupported form param %q"`（v1 非目标，明示失败不静默丢参数）
  - 实现 struct `<camel(serviceName)>Client`（字段 `c *vela.Client`）+ 构造函数 `New<Camel>Client(c *vela.Client) <Camel>`；方法体：`c.NewRequest(serviceID, method, prefix+path)` + path params + req body + `SetResult`
  - types import：解析 Dir 的 go.mod 取 module，拼 `<module>/internal/types`；无 go.mod 或布局异常 → 报错 `"vela-goctl: cannot derive types package from %s"`

- [ ] **Step 1: 写 fixture**

`goctl/testdata/order.api`：一个 service 块 `order-api`，两个 `@server` 组（含 prefix）、3 条 route（GET 带 path 参数、POST 带 body、GET 带 form 参数）。

- [ ] **Step 2: 写失败测试 `TestGenerate_Golden` / `TestDeriveTypesPkg`**

`gen_test.go` 用 go-zero `api/parser` 解析 fixture → 调 `gen(spec, dir)` 到临时目录 → 与 golden 文件逐字节对比。`TestDeriveTypesPkg`：临时 go.mod `module example.com/svc` → 得 `example.com/svc/internal/types`；无 go.mod → 报错串含 `cannot derive types package`。

- [ ] **Step 3: 跑测试确认失败**

Run: `cd goctl && go test ./... -v`
Expected: FAIL

- [ ] **Step 4: 实现 `main.go` + `gen.go` + `template.go`**

main 里 `plugin.NewPlugin()`，把 `Api` 交给 `gen`；golden 文件由实现产物人工核对后落盘（生成正确、编译通过为准）。

- [ ] **Step 5: 跑测试确认通过**

Run: `cd goctl && go test -race ./...`
Expected: PASS

- [ ] **Step 6: 编译验证生成代码可编译**

临时项目（module + go.mod require vela + goctl 生成 + 手写 types 包）`go build ./...` PASS；`go vet ./...` PASS。

- [ ] **Step 7: README 双语**

调用方式：`go build -o goctl-vela .` 放 PATH，然后 `goctl api plugin --plugin goctl-vela -api order.api -dir .`。

- [ ] **Step 8: 提交** `feat: add goctl plugin generating typed clients`

### Task 8: 文档与收尾

**Files:**
- Create: 根 `README.md` + `README.zh-CN.md`（仓库总览 + 三模块导航）
- Modify: `go.mod`/`go.work`（如依赖有漂移则整理）

- [ ] **Step 1: 根 README 双语**（是什么、三模块关系、quickstart 三段：core 手写调用 / eureka 接入 / goctl 生成）

- [ ] **Step 2: 全量验证**

Run: `go vet ./...`（根、eureka、goctl）；`go test -race ./...`（三模块）；`gofmt -l .` 输出为空
Expected: 全部 PASS、无未格式化文件

- [ ] **Step 3: 提交** `docs: add readmes and final polish`

- [ ] **Step 4: 提醒作者**：LICENSE 未定（公共库发布前必选）；resty v3 stable 发布后仅需改 core 内部 + go.mod。
