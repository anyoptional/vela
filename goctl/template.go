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
	"github.com/anyoptional/vela"
)

type {{.ServiceType}} interface {
	{{range .Routes}}
	{{.Name}}(ctx context.Context{{if .RequestType}}, req *types.{{.RequestType}}{{end}}) {{if .ResponseType}}(*types.{{.ResponseType}}, error){{else}}error{{end}}
	{{end}}
}

type {{.ClientType}} struct {
	c               *vela.Client
	fallback        {{.ServiceType}}
	fallbackFactory func(context.Context, *vela.Failure, *vela.Response) ({{.ServiceType}}, error)
	availability    func(*vela.Failure) bool
}

type {{.ClientConfigType}} struct {
	Target          vela.Target
	Discovery       vela.DiscoveryClient
	ClientOptions   []vela.Option
	Fallback        {{.ServiceType}}
	FallbackFactory func(context.Context, *vela.Failure, *vela.Response) ({{.ServiceType}}, error)
	Availability    func(*vela.Failure) bool
}

// defaultTarget is derived from the api file: the service block name is the
// service ID and the @server url annotation selects direct mode.
var defaultTarget = vela.Target{Name: {{quote .TargetName}}{{if .TargetURL}}, URL: {{quote .TargetURL}}{{end}}}

func {{.Constructor}}(config {{.ClientConfigType}}) ({{.ServiceType}}, error) {
	if config.Fallback != nil && config.FallbackFactory != nil {
		return nil, errors.New("vela: fallback and fallback factory are mutually exclusive")
	}
	target := config.Target
	if target.Name == "" {
		target = defaultTarget
	}
	options := append([]vela.Option{vela.WithDiscovery(config.Discovery)}, config.ClientOptions...)
	client, err := vela.New(target, options...)
	if err != nil {
		return nil, err
	}
	availability := config.Availability
	if availability == nil {
		availability = vela.DefaultAvailability
	}
	return &{{.ClientType}}{
		c:               client,
		fallback:        config.Fallback,
		fallbackFactory: config.FallbackFactory,
		availability:    availability,
	}, nil
}

func (c *{{.ClientType}}) resolveFallback(ctx context.Context, failure *vela.Failure, response *vela.Response) ({{.ServiceType}}, bool, error) {
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
			return nil, true, fmt.Errorf("vela: fallback factory: %w", err)
		}
		if fallback == nil {
			return nil, true, errors.New("vela: fallback factory returned nil service")
		}
	}
	return fallback, true, nil
}

{{range .Routes}}
func (c *{{$.ClientType}}) {{.Name}}(ctx context.Context{{if .RequestType}}, req *types.{{.RequestType}}{{end}}) {{if .ResponseType}}(*types.{{.ResponseType}}, error){{else}}error{{end}} {
	request := c.c.NewRequest({{.Method}}, {{quote .Path}})
	{{range .PathParams}}request.SetPathParam({{quote .Name}}, fmt.Sprint(req.{{.Field}}))
	{{end}}{{range .Headers}}request.SetHeader({{quote .Name}}, fmt.Sprint(req.{{.Field}}))
	{{end}}{{if .FormIsQuery}}{{range .FormParams}}request.SetQueryParam({{quote .Name}}, fmt.Sprint(req.{{.Field}}))
	{{end}}{{else}}{{range .FormParams}}request.SetFormParam({{quote .Name}}, fmt.Sprint(req.{{.Field}}))
	{{end}}{{end}}{{if .BodyFields}}request.SetBody(map[string]any{
		{{range .BodyFields}}{{quote .Name}}: req.{{.Field}},
		{{end}}})
	{{end}}{{if .ResponseType}}var result types.{{.ResponseType}}
	request.SetResult(&result)
	response, err := request.Do(ctx)
	if err != nil {
		var failure *vela.Failure
		if errors.As(err, &failure) {
			fallback, used, fallbackErr := c.resolveFallback(ctx, failure, response)
			if fallbackErr != nil {
				return nil, errors.Join(err, fallbackErr)
			}
			if used {
				result, fallbackErr := fallback.{{.Name}}(ctx{{if .RequestType}}, req{{end}})
				if fallbackErr != nil {
					return nil, errors.Join(err, fmt.Errorf("vela: fallback: %w", fallbackErr))
				}
				return result, nil
			}
		}
		return nil, err
	}
	return &result, nil
	{{else}}response, err := request.Do(ctx)
	if err != nil {
		var failure *vela.Failure
		if errors.As(err, &failure) {
			fallback, used, fallbackErr := c.resolveFallback(ctx, failure, response)
			if fallbackErr != nil {
				return errors.Join(err, fallbackErr)
			}
			if used {
				if fallbackErr := fallback.{{.Name}}(ctx{{if .RequestType}}, req{{end}}); fallbackErr != nil {
					return errors.Join(err, fmt.Errorf("vela: fallback: %w", fallbackErr))
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
