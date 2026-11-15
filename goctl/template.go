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
	"errors"
	"fmt"
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
	c               *jinx.Client
	fallback        {{.ServiceType}}
	fallbackFactory func(context.Context, *jinx.Failure, *jinx.Response) ({{.ServiceType}}, error)
	availability    func(*jinx.Failure) bool
}

type {{.ClientConfigType}} struct {
	Target          jinx.Target
	Discovery       jinx.DiscoveryClient
	ClientOptions   []jinx.Option
	Fallback        {{.ServiceType}}
	FallbackFactory func(context.Context, *jinx.Failure, *jinx.Response) ({{.ServiceType}}, error)
	Availability    func(*jinx.Failure) bool
}

func {{.Constructor}}(config {{.ClientConfigType}}) ({{.ServiceType}}, error) {
	if config.Fallback != nil && config.FallbackFactory != nil {
		return nil, errors.New("jinx: fallback and fallback factory are mutually exclusive")
	}
	options := append([]jinx.Option{jinx.WithDiscovery(config.Discovery)}, config.ClientOptions...)
	client, err := jinx.New(config.Target, options...)
	if err != nil {
		return nil, err
	}
	availability := config.Availability
	if availability == nil {
		availability = jinx.DefaultAvailability
	}
	return &{{.ClientType}}{
		c:               client,
		fallback:        config.Fallback,
		fallbackFactory: config.FallbackFactory,
		availability:    availability,
	}, nil
}

func (c *{{.ClientType}}) resolveFallback(ctx context.Context, failure *jinx.Failure, response *jinx.Response) ({{.ServiceType}}, bool, error) {
	if ctx.Err() != nil || !c.availability(failure) {
		return nil, false, nil
	}
	if c.fallback == nil && c.fallbackFactory == nil {
		return nil, false, nil
	}
	fallback := c.fallback
	if c.fallbackFactory != nil {
		var err error
		fallback, err = c.fallbackFactory(ctx, failure, response)
		if err != nil {
			return nil, true, fmt.Errorf("jinx: fallback factory: %w", err)
		}
		if fallback == nil {
			return nil, true, errors.New("jinx: fallback factory returned nil service")
		}
	}
	return fallback, true, nil
}

{{range .Routes}}
func (c *{{$.ClientType}}) {{.Name}}(ctx context.Context{{range .PathParams}}, {{.ArgName}} {{.Type}}{{end}}{{if .RequestType}}, req *types.{{.RequestType}}{{end}}) {{if .ResponseType}}(*types.{{.ResponseType}}, error){{else}}error{{end}} {
	request := c.c.NewRequest({{.Method}}, {{quote .Path}})
	{{range .PathParams}}request.SetPathParam({{quote .Name}}, fmt.Sprint({{.ArgName}}))
	{{end}}{{if .HasBody}}request.SetBody(req)
	{{end}}{{if .ResponseType}}var result types.{{.ResponseType}}
	request.SetResult(&result)
	response, err := request.Do(ctx)
	if err != nil {
		var failure *jinx.Failure
		if errors.As(err, &failure) {
			fallback, used, fallbackErr := c.resolveFallback(ctx, failure, response)
			if fallbackErr != nil {
				return nil, errors.Join(err, fallbackErr)
			}
			if used {
				result, fallbackErr := fallback.{{.Name}}(ctx{{range .PathParams}}, {{.ArgName}}{{end}}{{if .RequestType}}, req{{end}})
				if fallbackErr != nil {
					return nil, errors.Join(err, fmt.Errorf("jinx: fallback: %w", fallbackErr))
				}
				return result, nil
			}
		}
		return nil, err
	}
	return &result, nil
	{{else}}response, err := request.Do(ctx)
	if err != nil {
		var failure *jinx.Failure
		if errors.As(err, &failure) {
			fallback, used, fallbackErr := c.resolveFallback(ctx, failure, response)
			if fallbackErr != nil {
				return errors.Join(err, fallbackErr)
			}
			if used {
				if fallbackErr := fallback.{{.Name}}(ctx{{range .PathParams}}, {{.ArgName}}{{end}}{{if .RequestType}}, req{{end}}); fallbackErr != nil {
					return errors.Join(err, fmt.Errorf("jinx: fallback: %w", fallbackErr))
				}
				return nil
			}
		}
		return err
	}
	return nil
	{{end}}}

{{end}}`))

func executeClientTemplate(writer io.Writer, data clientTemplateData) error {
	return clientTemplate.Execute(writer, data)
}
