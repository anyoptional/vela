# jinx-goctl

`jinx-goctl` 是 goctl API 插件，用于生成强类型服务接口及基于 [`jinx`](https://github.com/anyoptional/jinx) 的默认客户端实现。插件会在指定目录生成 `client.go`。

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

service 块名会原样作为服务发现 ID，必须与 discovery client 暴露的服务 ID 一致。每个 `@handler` 会生成一个方法；path 参数根据对应 `path` tag 中声明的类型成为位置参数。请求和响应类型由 goctl 生成在 `internal/types`；GET 和 DELETE 方法不会发送请求体。暂不支持 form/query 参数，遇到时会明确返回错误。

例如，生成 `GetOrder` route 后，可在生成代码所属 package 中调用：

```go
api := client.NewOrderApiClient(jinxClient)
order, err := api.GetOrder(ctx, 42, &types.GetOrderReq{Id: 42})
```

Windows 下请使用 `go build -o goctl-jinx.exe .`，并确保 goctl 能找到该可执行文件。
