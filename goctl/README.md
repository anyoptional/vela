# jinx-goctl

`jinx-goctl` is a goctl API plugin that generates a typed service interface and a default client backed by [`jinx`](https://github.com/anyoptional/jinx). It emits `client.go` in the directory supplied to the plugin.

## Build and run

Build the plugin and put the binary on `PATH`:

```sh
cd goctl
go build -o goctl-jinx .
```

Then generate go-zero types and the jinx client:

```sh
goctl api go -api order.api -dir .
goctl api plugin --plugin goctl-jinx -api order.api -dir .
```

The target directory must contain the module's `go.mod`; the generator uses its module path to import `internal/types`. If the directory already has non-test Go files, the generated file uses their package name; otherwise it uses package `client`.

The service block name is the discovery service ID, unchanged. It must match the service ID exposed by the configured discovery client. Each `@handler` becomes a method. Path parameters become positional arguments with the types declared by their `path` tags. Request/response types come from goctl's generated `internal/types`; GET and DELETE methods do not send a body. Form/query parameters are not supported and produce an explicit error.

For example, after generation a route with handler `GetOrder` can be called from within the generated package:

```go
api := client.NewOrderApiClient(jinxClient)
order, err := api.GetOrder(ctx, 42, &types.GetOrderReq{Id: 42})
```

On Windows, use `go build -o goctl-jinx.exe .` and make that executable available to goctl.
