package main

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"go/format"
	goparser "go/parser"
	"go/token"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zeromicro/go-zero/tools/goctl/api/gogen"
	"github.com/zeromicro/go-zero/tools/goctl/api/spec"
	namingformat "github.com/zeromicro/go-zero/tools/goctl/util/format"
	"github.com/zeromicro/go-zero/tools/goctl/util/stringx"
)

type clientTemplateData struct {
	Package          string
	ServiceType      string
	ClientType       string
	ClientConfigType string
	Constructor      string
	TypesPackage     string
	FileBase         string
	TargetName       string
	TargetURL        string
	HasTypes         bool
	Routes           []routeTemplateData
}

type routeTemplateData struct {
	Name         string
	Method       string
	Path         string
	RequestType  string
	ResponseType string
	PathParams   []taggedField
	Headers      []taggedField
	FormParams   []taggedField
	FormIsQuery  bool
	BodyFields   []bodyField
}

// taggedField maps a request struct field to a path/header/form name.
type taggedField struct {
	Name  string
	Field string
}

// bodyField maps a request struct field to a JSON body key.
type bodyField struct {
	Name  string
	Field string
}

func gen(api *spec.ApiSpec, dir, style string) error {
	if api == nil {
		return errors.New("jinx-goctl: API spec is nil")
	}
	if api.Service.Name == "" {
		return errors.New("jinx-goctl: service name is empty")
	}
	fileBase, err := namingformat.FileNamingFormat(cmp.Or(style, "go_zero"), strings.ReplaceAll(api.Service.Name, "-", "_"))
	if err != nil {
		return fmt.Errorf("jinx-goctl: file naming style %q: %w", style, err)
	}
	if fileBase == "" || fileBase == "." || fileBase == ".." || strings.ContainsAny(fileBase, `/\`) {
		return fmt.Errorf("jinx-goctl: invalid file base name %q for service %q", fileBase, api.Service.Name)
	}

	serviceType := toCamel(api.Service.Name)
	if !token.IsIdentifier(serviceType) {
		return fmt.Errorf("jinx-goctl: invalid service name %q", api.Service.Name)
	}
	packageName, err := directoryPackage(dir)
	if err != nil {
		return err
	}

	data := clientTemplateData{
		Package:          packageName,
		ServiceType:      serviceType,
		ClientType:       stringx.From(serviceType).Untitle() + "Client",
		ClientConfigType: serviceType + "ClientConfig",
		Constructor:      "New" + serviceType + "Client",
		FileBase:         fileBase,
	}
	if !token.IsIdentifier(data.ClientType) || !token.IsIdentifier(data.ClientConfigType) || !token.IsIdentifier(data.Constructor) {
		return fmt.Errorf("jinx-goctl: invalid generated client name for service %q", api.Service.Name)
	}

	targetURL := ""
	for _, group := range api.Service.Groups {
		if url := group.Annotation.Properties["url"]; url != "" {
			if targetURL != "" && targetURL != url {
				return fmt.Errorf("jinx-goctl: conflicting url annotations: %q vs %q", targetURL, url)
			}
			targetURL = url
		}
		prefix := group.Annotation.Properties[spec.RoutePrefixKey]
		for _, route := range group.Routes {
			generated, err := generateRoute(prefix, route)
			if err != nil {
				return err
			}
			data.Routes = append(data.Routes, generated)
			data.HasTypes = data.HasTypes || generated.RequestType != "" || generated.ResponseType != ""
		}
	}
	data.TargetName = api.Service.Name
	data.TargetURL = targetURL
	if len(data.Routes) == 0 {
		return errors.New("jinx-goctl: service has no routes")
	}
	if data.HasTypes {
		typesPackage, err := deriveTypesPkg(dir)
		if err != nil {
			return err
		}
		data.TypesPackage = typesPackage
		if err := writeTypesFile(api, dir, data.FileBase); err != nil {
			return err
		}
	}

	var source bytes.Buffer
	if err := executeClientTemplate(&source, data); err != nil {
		return fmt.Errorf("jinx-goctl: render client: %w", err)
	}
	formatted, err := format.Source(source.Bytes())
	if err != nil {
		return fmt.Errorf("jinx-goctl: format generated client: %w", err)
	}
	clientFile := filepath.Join(dir, data.FileBase+".go")
	if err := os.WriteFile(clientFile, formatted, 0o644); err != nil {
		return fmt.Errorf("jinx-goctl: write %s: %w", clientFile, err)
	}
	return nil
}

// writeTypesFile generates types/<fileBase>.go from the API spec's type
// declarations, reusing goctl's own type generator so the output matches
// goctl conventions exactly. The types subpackage is exported (not
// internal/): contract modules are imported by other modules, and Go's
// internal rule would forbid importing internal/types from outside.
// The plugin is self-contained: callers do not need to run "goctl api go".
func writeTypesFile(api *spec.ApiSpec, dir, fileBase string) error {
	typesDir := filepath.Join(dir, "types")
	if err := os.MkdirAll(typesDir, 0o755); err != nil {
		return fmt.Errorf("jinx-goctl: create types directory: %w", err)
	}
	declarations, err := gogen.BuildTypes(api.Types)
	if err != nil {
		return fmt.Errorf("jinx-goctl: build types: %w", err)
	}
	formatted, err := format.Source([]byte("package types\n\n" + declarations))
	if err != nil {
		return fmt.Errorf("jinx-goctl: format generated types: %w", err)
	}
	typesFile := filepath.Join(typesDir, fileBase+".go")
	if err := os.WriteFile(typesFile, formatted, 0o644); err != nil {
		return fmt.Errorf("jinx-goctl: write %s: %w", typesFile, err)
	}
	return nil
}

func generateRoute(prefix string, route spec.Route) (routeTemplateData, error) {
	methodName := toCamel(route.Handler)
	if !token.IsIdentifier(methodName) {
		return routeTemplateData{}, fmt.Errorf("jinx-goctl: invalid handler name %q", route.Handler)
	}

	method, ok := httpMethods[strings.ToUpper(route.Method)]
	if !ok {
		return routeTemplateData{}, fmt.Errorf("jinx-goctl: unsupported HTTP method %q", route.Method)
	}

	generated := routeTemplateData{
		Name:   methodName,
		Method: method,
		Path:   path.Join(prefix, route.Path),
	}
	switch strings.ToUpper(route.Method) {
	case http.MethodGet, http.MethodDelete, http.MethodHead, http.MethodOptions:
		generated.FormIsQuery = true
	}

	if route.RequestType != nil {
		generated.RequestType = route.RequestType.Name()
		if !token.IsIdentifier(generated.RequestType) {
			return routeTemplateData{}, fmt.Errorf("jinx-goctl: unsupported request type %q", generated.RequestType)
		}
		if err := generated.classifyRequestFields(route.Path, route.RequestType); err != nil {
			return routeTemplateData{}, err
		}
	}
	if route.ResponseType != nil {
		generated.ResponseType = route.ResponseType.Name()
		if !token.IsIdentifier(generated.ResponseType) {
			return routeTemplateData{}, fmt.Errorf("jinx-goctl: unsupported response type %q", generated.ResponseType)
		}
	}
	return generated, nil
}

// classifyRequestFields partitions the request struct's fields by their
// goctl tags: path → SetPathParam, header → SetHeader, form/query →
// SetQueryParam or SetFormParam, json (or untagged) → JSON body.
func (generated *routeTemplateData) classifyRequestFields(routePath string, requestType spec.Type) error {
	pathParamNames := make(map[string]bool)
	for segment := range strings.SplitSeq(routePath, "/") {
		if name, ok := strings.CutPrefix(segment, ":"); ok && name != "" {
			pathParamNames[name] = true
		}
	}
	matchedPath := make(map[string]bool)
	jsonSeen := false
	formSeen := false

	for _, member := range structMembers(requestType) {
		tags, err := spec.Parse(member.Tag)
		if err != nil {
			return fmt.Errorf("jinx-goctl: parse tags for request field %q: %w", member.Name, err)
		}
		kind, name := memberTagKind(tags, member.Name)
		switch kind {
		case "path":
			if !pathParamNames[name] {
				return fmt.Errorf("jinx-goctl: request field %q has path tag %q not present in route %q", member.Name, name, routePath)
			}
			if !isScalarType(member.Type) {
				return fmt.Errorf("jinx-goctl: path field %q must be a scalar type", member.Name)
			}
			matchedPath[name] = true
			generated.PathParams = append(generated.PathParams, taggedField{Name: name, Field: member.Name})
		case "header":
			if !isScalarType(member.Type) {
				return fmt.Errorf("jinx-goctl: header field %q must be a scalar type", member.Name)
			}
			generated.Headers = append(generated.Headers, taggedField{Name: name, Field: member.Name})
		case "form":
			formSeen = true
			if !isScalarType(member.Type) {
				return fmt.Errorf("jinx-goctl: form field %q must be a scalar type", member.Name)
			}
			generated.FormParams = append(generated.FormParams, taggedField{Name: name, Field: member.Name})
		default:
			jsonSeen = true
			generated.BodyFields = append(generated.BodyFields, bodyField{Name: name, Field: member.Name})
		}
	}

	if jsonSeen && formSeen {
		return fmt.Errorf("jinx-goctl: request type %q mixes json and form fields", requestType.Name())
	}
	for name := range pathParamNames {
		if !matchedPath[name] {
			return fmt.Errorf("jinx-goctl: cannot determine path field for path param %q", name)
		}
	}
	return nil
}

// memberTagKind returns the goctl tag kind and its name for a member.
// Untagged members are treated as JSON body fields named after the member.
func memberTagKind(tags *spec.Tags, memberName string) (kind, name string) {
	for _, key := range []string{"path", "header", "form", "query", "json"} {
		tag, err := tags.Get(key)
		if err == nil && tag != nil {
			kind := key
			if key == "query" {
				kind = "form"
			}
			return kind, cmp.Or(tag.Name, memberName)
		}
	}
	return "json", memberName
}

func isScalarType(tp spec.Type) bool {
	switch tp.(type) {
	case spec.PrimitiveType, *spec.PrimitiveType:
		return true
	default:
		return false
	}
}

func structMembers(requestType spec.Type) []spec.Member {
	switch typed := requestType.(type) {
	case spec.DefineStruct:
		return typed.Members
	case *spec.DefineStruct:
		return typed.Members
	case spec.NestedStruct:
		return typed.Members
	case *spec.NestedStruct:
		return typed.Members
	default:
		return nil
	}
}

// deriveTypesPkg resolves the import path of the generated types package.
// It walks upward from dir to the nearest go.mod: the module root
// contributes the module path, and the relative path from the module root
// to dir becomes the package suffix, so generation works in subdirectories
// of the module too.
func deriveTypesPkg(dir string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("jinx-goctl: resolve output directory %s: %w", dir, err)
	}
	for current := absDir; ; current = filepath.Dir(current) {
		content, err := os.ReadFile(filepath.Join(current, "go.mod"))
		if err == nil {
			modulePath := modulePathFrom(content)
			if modulePath == "" {
				break
			}
			rel, relErr := filepath.Rel(current, absDir)
			if relErr != nil {
				break
			}
			return path.Join(modulePath, filepath.ToSlash(rel), "types"), nil
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return "", fmt.Errorf("jinx-goctl: cannot derive types package from %s", dir)
}

func modulePathFrom(content []byte) string {
	for line := range strings.SplitSeq(string(content), "\n") {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "module" {
			continue
		}
		if len(fields) != 2 {
			return ""
		}
		return strings.Trim(fields[1], `"`)
	}
	return ""
}

func directoryPackage(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("jinx-goctl: read output directory %s: %w", dir, err)
	}
	var packageName string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := goparser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, goparser.PackageClauseOnly)
		if err != nil {
			return "", fmt.Errorf("jinx-goctl: read package from %s: %w", name, err)
		}
		if packageName != "" && file.Name.Name != packageName {
			return "", fmt.Errorf("jinx-goctl: multiple Go packages in output directory %s", dir)
		}
		packageName = file.Name.Name
	}
	packageName = cmp.Or(packageName, "client")
	if !token.IsIdentifier(packageName) {
		return "", fmt.Errorf("jinx-goctl: invalid package name %q", packageName)
	}
	return packageName, nil
}

var httpMethods = map[string]string{
	http.MethodGet:     "http.MethodGet",
	http.MethodPost:    "http.MethodPost",
	http.MethodPut:     "http.MethodPut",
	http.MethodPatch:   "http.MethodPatch",
	http.MethodDelete:  "http.MethodDelete",
	http.MethodHead:    "http.MethodHead",
	http.MethodOptions: "http.MethodOptions",
	http.MethodConnect: "http.MethodConnect",
	http.MethodTrace:   "http.MethodTrace",
}

func quoted(value string) string {
	return strconv.Quote(value)
}

func toCamel(value string) string {
	return stringx.From(strings.ReplaceAll(value, "-", "_")).ToCamel()
}
