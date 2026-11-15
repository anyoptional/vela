package main

import (
	"io"
	"text/template"
)

var clientTemplate = template.Must(template.New("client").Funcs(template.FuncMap{
	"quote": quoted,
}).Parse(`package {{.Package}}

import (
	"context"
	{{if .HasPathArgs}}"fmt"{{end}}
	"net/http"
{{if .HasTypes}}
	types "{{.TypesPackage}}"{{end}}
	"github.com/anyoptional/jinx"
)

type {{.ServiceType}} interface {
	{{range .Routes}}
	{{.Name}}(ctx context.Context{{range .PathParams}}, {{.ArgName}} {{.Type}}{{end}}{{if .RequestType}}, req *types.{{.RequestType}}{{end}}) {{if .ResponseType}}(*types.{{.ResponseType}}, error){{else}}error{{end}}
	{{end}}
}

type {{.ClientType}} struct {
	c *jinx.Client
}

func {{.Constructor}}(c *jinx.Client) {{.ServiceType}} {
	return &{{.ClientType}}{c: c}
}

{{range .Routes}}
func (c *{{$.ClientType}}) {{.Name}}(ctx context.Context{{range .PathParams}}, {{.ArgName}} {{.Type}}{{end}}{{if .RequestType}}, req *types.{{.RequestType}}{{end}}) {{if .ResponseType}}(*types.{{.ResponseType}}, error){{else}}error{{end}} {
	request := c.c.NewRequest({{.Method}}, {{quote .Path}})
	{{range .PathParams}}request.SetPathParam({{quote .Name}}, fmt.Sprint({{.ArgName}}))
	{{end}}{{if .HasBody}}request.SetBody(req)
	{{end}}{{if .ResponseType}}var result types.{{.ResponseType}}
	request.SetResult(&result)
	if _, err := request.Do(ctx); err != nil {
		return nil, err
	}
	return &result, nil
	{{else}}if _, err := request.Do(ctx); err != nil {
		return err
	}
	return nil
	{{end}}}

{{end}}`))

func executeClientTemplate(writer io.Writer, data clientTemplateData) error {
	return clientTemplate.Execute(writer, data)
}
