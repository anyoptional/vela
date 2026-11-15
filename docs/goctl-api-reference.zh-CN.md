# goctl API 文件参考手册

本文按仓库锁定的 go-zero/goctl `v1.10.1` 源码整理，并将“goctl 基础语法”和“Swagger 插件如何使用这些语法”分开说明。基础语法部分讲 `.api` parser 接受什么；Swagger 部分讲 `goctl api swagger` 实际读取什么、生成什么。两者不等同：语法能解析，不代表 Swagger 会消费该字段。

## 第一部分：goctl 支持的基础语法

### 1. 文件结构

一个 `.api` 文件可包含：

1. `syntax`：API 语法版本。
2. `import`：导入其他 API 文件。
3. `info`：API 文件级 key-value 元数据。
4. `type`：数据类型声明。
5. 服务级 `@server(...)` 和 `service`：服务元信息及路由。

Grammar 使用 `spec*`，这些声明不强制固定顺序；为便于阅读，建议按上面顺序书写。

```api
syntax = "v1"

import (
    "common.api"
)

info(
    title: "Order API"
    version: "1.0"
)

type CreateOrderReq {
    Name string `json:"name"`
}

type CreateOrderResp {
    Id int64 `json:"id"`
}

@server(
    prefix: /api/v1
    group: order
)
service order-api {
    @doc "创建订单"
    @handler CreateOrder
    post /orders (CreateOrderReq) returns (CreateOrderResp)
}
```

### 2. `syntax`、`import` 和注释

#### `syntax`

```api
syntax = "v1"
```

- 版本必须是带引号的 `vN`，例如 `"v1"`。
- 每个 API 文件最多声明一次；未写时按 v1 处理。
- 主文件和导入文件应使用相同的 syntax 版本。

#### `import`

支持单个导入或导入块：

```api
import "common.api"

import (
    "types/user.api"
    "types/order.api"
)
```

路径以 `.api` 结尾。同一路径不能重复导入；被导入文件中的声明会与主文件合并，因此类型、路由等不能冲突。v1.10.1 parser 不支持被导入文件再嵌套 `import`。

#### 注释

支持 `//` 单行注释和 `/* ... */` 块注释。普通注释用于补充说明；`@doc` 是独立的路由注解，不是普通注释。

### 3. `info(...)` 基础形式

基础语法是一个或多个 `key: value` 项（不能是空的 `info()`）。key 是标识符；value 可以是不带引号的单行值，也可以使用双引号。包含空格或换行时建议加引号。

```api
info(
    title: "Order API"
    description: "订单服务"
    customKey: customValue
)
```

Parser 允许自定义 key；它只负责解析和保存 metadata，不规定每个 key 的含义。每个 API 文件最多一个 `info`，key 不要重复。Swagger 对 `info` 的具体解释见第二部分。

### 4. `type` 和字段

#### 结构体声明

支持单个结构体和类型块：

```api
type User {
    Id   int64  `json:"id"`
    Name string `json:"name"`
}

type (
    CreateUserReq {
        Name string `json:"name"`
    }

    CreateUserResp {
        Id int64 `json:"id"`
    }
)
```

`struct` 关键字可选，也可写为 `type User struct { ... }`。

#### 基础类型形式

字段可使用以下类型形式：

- Go 基本类型：`bool`、`int`、`int8`、`int16`、`int32`、`int64`、`uint`、`uint8`、`uint16`、`uint32`、`uint64`、`uintptr`、`float32`、`float64`、`complex64`、`complex128`、`string`、`byte`、`rune`。
- 已定义的命名类型。
- 指针：`*User`。
- 切片：`[]string`、`[]User`、`[]*User`。
- map：`map[string]int` 等，map key 必须是 Go 基本类型。
- `interface{}`。
- 嵌套结构体和匿名嵌入字段。

注意：语法可识别的类型不一定都能通过后续校验。**类型别名不受支持**；`time.Time` 在 v1.10.1 中也会被明确拒绝。

#### 字段 tag 的基础形式

字段 tag 使用 Go struct tag 的反引号形式：

```api
type Request {
    Id    int64  `path:"id"`
    Name  string `form:"name,optional"`
    Token string `header:"Authorization"`
}
```

Parser 的 tag 词法规则允许小写 tag key 和带引号的 value。它不会因为某个自定义 tag key 而拒绝语法；tag 的具体 key 和选项含义由 goctl 下游生成器决定。Swagger 使用的 tag 见第二部分。

goctl 标准 HTTP 代码生成常见的字段 tag 有 `json`（JSON body 字段）、`path`（路径参数）、`form`（表单或 query 字段）和 `header`（请求头）。各类生成器对它们的处理有差别；Swagger 的具体映射见第二部分。

### 5. `service`、路由和 handler

#### `service`

```api
service order-api {
    @handler GetOrder
    get /orders/:id (GetOrderReq) returns (GetOrderResp)
}
```

服务名可由一个或多个标识符段以 `-` 连接，例如 `order-api`。同一个 API 的多个 `service` 声明需要使用相同服务名。

#### 路由

路由的基本形式：

```text
HTTP方法 路径 [(请求类型)] [returns (响应类型)]
```

例如：

```api
// 无请求体和响应体
@handler Ping
get /ping

// 有请求体和响应体
@handler CreateOrder
post /orders (CreateOrderReq) returns (CreateOrderResp)

// 路径参数
@handler GetOrder
get /orders/:id (GetOrderReq) returns (GetOrderResp)
```

- HTTP method 必须小写。基础 parser 接受 `get`、`head`、`post`、`put`、`patch`、`delete`、`connect`、`options`、`trace`。
- 路径可为 `/` 或分段形式，例如 `/orders`、`/orders/:id`、`/order-items/:item-id`。
- 路径参数以 `:` 开头，例如 `/:id`。
- 根路径 `/` 合法；普通路径不应以 `/` 结尾。
- 请求体类型写在括号中；不需要请求体时省略，空括号 `()` 已弃用。
- 响应体使用 `returns (Type)`；不需要响应体时省略 `returns`，`returns ()` 已弃用。
- 请求体应为已定义的普通结构体类型，不能写指针类型。
- 响应可使用命名类型；数组响应属于兼容形式，建议优先使用结构体响应。
- 重复的 HTTP method + path、重复 handler 会报错。

#### `@handler`

每条路由都必须指定 handler，注解放在路由之前：

```api
@handler GetOrder
get /orders/:id
```

也可以通过路由级 `@server` 的 `handler` key 指定，两种方式二选一：

```api
@server(
    handler: GetOrder
)
get /orders/:id
```

服务级 `@server` 中的 `handler` 不能替代逐路由的 handler。Handler 不应在同一 group 中重名。

### 6. `@server(...)` 和 `@doc` 的基础语法

#### `@server(...)`

`@server` 可以修饰 `service`，也可以修饰路由；每个块必须至少包含一个 `key: value`：

```api
@server(
    prefix: /api/v1
    group: order
)
service order-api {
    @server(
        handler: GetOrder
    )
    get /orders/:id
}
```

`@server` 的 key **可以自定义**。Parser 接受 key-value 并保存 metadata，但自定义 key 不会自动触发标准生成行为；标准生成器和插件各自决定识别哪些 key。不要在同一个 `@server` 块重复 key。

goctl 标准 HTTP server 代码生成器识别的常见 key：

| 作用域 | Key | 含义 |
|---|---|---|
| service | `prefix` | 路由统一前缀 |
| service | `group` | 生成代码分组 |
| service | `jwt` | JWT 鉴权逻辑 |
| service | `jwtTransition` | JWT 迁移兼容配置 |
| service | `middleware` | 中间件名称，逗号分隔 |
| service | `timeout` | 请求超时，使用 Go duration 格式，如 `5s` |
| service | `maxBytes` | 请求体大小上限，数字格式 |
| service | `signature` | `true` 时启用签名相关逻辑 |
| service | `sse` | `true` 时启用 SSE 相关逻辑 |
| route | `handler` | 指定该路由的 handler |
| route | `group` | 为该路由指定代码分组 |

例如，`middleware: Auth,Audit` 中间件名称以逗号分隔。表格之外的 key 可以被解析，但不会自动获得 goctl 标准代码生成行为。Swagger 还会读取其他服务级 key，见第二部分。

#### `@doc`

`@doc` 可用字符串形式或 key-value 形式，且放在路由前：

```api
@doc "获取订单"
@handler GetOrder
get /orders/:id
```

```api
@doc(
    summary: "获取订单"
    description: "根据订单 ID 查询"
)
@handler GetOrder
get /orders/:id
```

字符串必须使用双引号。key-value 形式可写在括号内；语法也允许省略括号。Key-value 的 key 可扩展；Parser 会读取并保存它们，但不会规定所有 key 的语义。字符串和 key-value 的具体 Swagger 行为见第二部分。

### 7. goctl 基础语法校验要点

- `syntax`、`info` 不能重复；`info` key 和 `@server` key 不要重复。
- 导入路径、命名类型、service 名称、handler 和路由有重复检查。
- 每条路由必须有 handler。
- `@doc` 要放在路由注解之前；建议顺序为 `@doc` → `@handler` 或路由级 `@server` → 路由。
- Parser 接受的 method、类型或自定义 metadata，不代表每个下游生成器都能消费。

## 第二部分：Swagger 插件支持的 API 语法

本部分只列 goctl v1.10.1 Swagger 生成器实际读取的语法和字段。插件生成 Swagger 2.0 文档；Swagger 未读取的自定义 key 不会因此出现在文档中。

### 1. Swagger 支持的 HTTP methods 和路径

Swagger 路由生成器输出 `GET`、`HEAD`、`POST`、`PUT`、`PATCH`、`DELETE`、`OPTIONS` 操作。基础 parser 接受的 `CONNECT`、`TRACE` 不会输出 Swagger operation。

路径中的 `:id` 会转换为 OpenAPI/Swagger 路径参数形式 `{id}`。服务级 `prefix` 会加到 Swagger 路径前。

### 2. Swagger 对 `info(...)` 的完整 key 清单

v1.10.1 Swagger 生成器读取以下 `info` key：

| Key | Swagger 用途 | 默认或说明 |
|---|---|---|
| `title` | `info.title` | — |
| `description` | `info.description` | — |
| `termsOfService` | `info.termsOfService` | — |
| `version` | `info.version` | 默认 `1.0` |
| `contactName` | 联系人名称 | 与 URL、Email 一起生成 contact |
| `contactURL` | 联系人 URL | 同上 |
| `contactEmail` | 联系人 Email | 同上 |
| `licenseName` | license 名称 | 与 `licenseURL` 一起生成 license |
| `licenseURL` | license URL | 同上 |
| `consumes` | 全局请求媒体类型 | 默认 `application/json`；逗号分隔 |
| `produces` | 全局响应媒体类型 | 默认 `application/json`；逗号分隔 |
| `schemes` | 全局协议 | 默认 `https`；逗号分隔 |
| `host` | Swagger host | 默认空值 |
| `basePath` | Swagger basePath | 默认 `/` |
| `securityDefinitionsFromJson` | JSON 解码为 Swagger `securityDefinitions` | 内容应是 JSON 对象 |
| `useDefinitions` | 是否在响应和 JSON 请求体使用 definitions 引用 | 默认 `false` |
| `wrapCodeMsg` | 是否把响应 schema 包装成 `{"code":0,"msg":"OK","data":...}` | 默认 `false` |
| `bizCodeEnumDescription` | 全局业务码描述 | `wrapCodeMsg` 开启时使用；默认 `business code` |

列表类值以逗号分隔；`wrapCodeMsg`、`useDefinitions` 使用布尔值。安全配置示例：

```api
info(
    securityDefinitionsFromJson: `{"apiKey":{"type":"apiKey","name":"x-api-key","in":"header"}}`
)
```

### 3. Swagger 对服务级 `@server(...)` 的完整 key 清单

Swagger 从**服务级** `@server` 读取以下 key：

| Key | Swagger 用途 |
|---|---|
| `prefix` | 组合到所有路由路径前 |
| `tags` | 设置 operation 的 Swagger tags，多个值用逗号分隔 |
| `summary` | 当 `tags` 未设置时，作为 tags 的后备值 |
| `group` | 与 handler 名组合生成 `operationId` |
| `authType` | 设置该 group 的 Swagger security 要求；值须与 `securityDefinitionsFromJson` 中定义的名称相同 |

示例：

```api
@server(
    tags: "订单"
    prefix: /api/v1
    group: order
    authType: apiKey
)
service order-api {
    @doc "获取订单"
    @handler GetOrder
    get /orders/:id
}
```

区分以下几点：

- `authType` 只描述 Swagger security，不等同于 go-zero 服务端 JWT 鉴权；生成服务端 JWT 逻辑使用的是基础代码生成器的 `jwt`。
- Swagger 读取的是服务级 `@server`；路由级 `@server(handler: ...)` 是基础路由 handler 声明，不作为 Swagger 的 group 配置。
- v1.10.1 源码定义了 `operationId` 常量，但当前 Swagger operation 实际由 handler 和 `group` 生成，不读取 `operationId` key。
- `@server` 语法允许自定义 key，但上述表格之外的 key 不会自动映射到 Swagger。

### 4. Swagger 对路由 `@doc` 的完整 key 清单

字符串 `@doc` 可作为 Swagger operation summary 的后备文案；未提供时再回退到 handler 名称。键值形式支持以下 Swagger key：

| Key | Swagger 用途 |
|---|---|
| `summary` | operation summary；优先级高于字符串形式 |
| `description` | operation description |
| `produces` | 此 operation 的响应媒体类型，逗号分隔 |
| `schemes` | 此 operation 的协议列表，逗号分隔 |
| `deprecated` | operation 的废弃标志，使用 `true`/`false` |
| `externalDocsDescription` | 外部文档描述 |
| `externalDocsURL` | 外部文档 URL |
| `bizCodeEnumDescription` | `wrapCodeMsg` 开启时覆盖当前 operation 的业务码说明 |

示例：

```api
@doc(
    summary: "获取订单"
    description: "根据订单 ID 查询订单详情"
    produces: application/json, application/xml
    schemes: https
    deprecated: false
    externalDocsDescription: "订单 API 文档"
    externalDocsURL: "https://example.com/orders"
    bizCodeEnumDescription: "1001-未登录<br>1002-无权访问"
)
@handler GetOrder
get /orders/:id (GetOrderReq) returns (GetOrderResp)
```

`consumes` 是全局 `info` 配置，不是当前 Swagger operation 实际读取的 `@doc` key。`@doc` 中未列出的扩展 key 虽能被基础 parser 保存，但不会自动写入 Swagger。

### 5. Swagger 对请求字段 tag 和选项的支持

Swagger 根据请求结构体字段 tag 生成参数。常用映射如下：

| 字段 tag | Swagger 参数位置 |
|---|---|
| `path:"id"` | path parameter |
| `header:"Authorization"` | header parameter |
| `form:"name"` | GET 时为 query parameter；非 GET 时为 formData parameter |
| `json:"name"` | JSON body schema/property |

示例：

```api
type SearchReq {
    Id      int64  `path:"id,range=[1:10000],example=10"`
    Keyword string `form:"keyword,optional,example=vela"`
    Token   string `header:"Authorization"`
}

type CreateReq {
    Name string `json:"name,example=coffee"`
}
```

Swagger 会读取 tag 选项：

| 选项 | Swagger 用途 |
|---|---|
| `optional` | 将 body、form、path 字段/参数标记为非必需；Swagger 当前不会将它用于 header 的 required 判断 |
| `options=a\|b\|c` | 生成 enum |
| `range=[min:max]` | 生成数值上下限；可用 `[`/`(` 和 `]`/`)` 表示闭区间或开区间，边界可留空 |
| `default=value` | 生成默认值 |
| `example=value` | 生成示例值；未指定 example 时可回退到 default |

注意：

- `omitempty` 是常见 Go JSON tag 选项，但不是 v1.10.1 Swagger 插件读取的参数/schema 选项。
- Swagger 的类型转换将整数类型（含 `byte`）映射为 `integer`，`float32`/`float64` 为 `number`，`string` 为 `string`，`bool` 为 `boolean`；数组映射为 `array`，结构体和 map 映射为 `object`。
- 基础 parser 接受但 Swagger 类型映射未定义的类型，不能据此假定会生成有效的 Swagger schema；例如 `complex64`、`complex128`、`uintptr`、`rune` 没有对应的 Swagger primitive 映射。
- `useDefinitions: true` 时，Swagger 可为响应和 JSON 请求体生成 `definitions` 并用 `$ref` 引用。
- operation 级 `consumes` 会按请求字段和 method 推断：GET 不设置 consumes；非 GET 请求含 `json` 字段时使用 JSON，否则使用 form-urlencoded。`info.consumes` 仍用于 Swagger 文档的全局 consumes。
- Swagger 生成器把 `json` 响应 schema 放在 HTTP 200 response 下；`.api` 当前没有单独指定响应状态码的 route 注解。

### 6. Swagger 完整示例

go-zero v1.10.1 自带的 Swagger 示例覆盖了全局 `info`、安全定义、服务级 `@server`、query/path/form/JSON 请求、字段选项、嵌套类型和复杂响应，可作为综合样例：

- [中文完整示例 example_cn.api](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/swagger/example/example_cn.api)
- [英文完整示例 example.api](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/swagger/example/example.api)

## 本仓库 `goctl-vela` 插件的额外限制

本仓库的 [goctl 插件 README](../goctl/README.md) 说明：插件为每个 handler 生成 typed-client 方法，使用 URL path 参数及请求/响应类型；目前不支持 form/query 参数，并会明确报错。它是一个 goctl 插件，不等同于 goctl 自带的 HTTP server 代码生成器或 Swagger 生成器。

## 源码索引

以下链接固定到 `go-zero` 仓库的 `tools/goctl/v1.10.1` 源码：

- [API parser 语法 ApiParser.g4](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/parser/g4/ApiParser.g4)
- [词法规则 ApiLexer.g4](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/parser/g4/ApiLexer.g4)
- [service、route、`@doc` AST](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/parser/g4/ast/service.go)
- [API 文件级重复项与校验](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/parser/g4/ast/api.go)
- [HTTP 方法与 tag 词法校验](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/parser/g4/gen/api/baseparser.go)
- [AST 转换为 goctl spec](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/parser/parser.go)
- [类型与字段解析](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/parser/g4/ast/type.go)
- [基础字段 tag 解析](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/spec/tags.go)
- [goctl HTTP server 路由生成](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/gogen/genroutes.go)
- [Swagger 配置 key](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/swagger/const.go)
- [Swagger OpenAPI info 与结构](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/swagger/swagger.go)
- [Swagger 路由、server 注解和 `@doc`](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/swagger/path.go)
- [Swagger 请求参数及字段 tag](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/swagger/parameter.go)
- [Swagger 结构体属性](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/swagger/properties.go)
- [Swagger tag 选项解析](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/swagger/options.go)
- [Swagger 响应生成](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/swagger/response.go)
- [Swagger consumes 推断](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/swagger/contenttype.go)
- [Swagger 完整中文 API 示例](https://github.com/zeromicro/go-zero/blob/tools/goctl/v1.10.1/tools/goctl/api/swagger/example/example_cn.api)
