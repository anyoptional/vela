package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zeromicro/go-zero/tools/goctl/api/parser"
	"github.com/zeromicro/go-zero/tools/goctl/api/spec"
)

func TestGenerate_Golden(t *testing.T) {
	api := parseFixture(t)
	api.Service.Groups = api.Service.Groups[:1]

	dir := t.TempDir()
	writeModuleFile(t, dir)
	if err := gen(api, dir); err != nil {
		t.Fatalf("gen(): %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "client.go"))
	if err != nil {
		t.Fatalf("ReadFile(client.go): %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "client.golden"))
	if err != nil {
		t.Fatalf("ReadFile(client.golden): %v", err)
	}
	if !bytes.Equal(normalizeLineEndings(got), normalizeLineEndings(want)) {
		t.Errorf("generated client differs from golden\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestGenerate_UnsupportedFormParam(t *testing.T) {
	dir := t.TempDir()
	writeModuleFile(t, dir)
	err := gen(parseFixture(t), dir)
	if err == nil || !strings.Contains(err.Error(), `jinx-goctl: unsupported form param "status"`) {
		t.Fatalf("gen() error = %v, want unsupported form param status", err)
	}
}

func TestGenerate_DeleteHasNoBody(t *testing.T) {
	api := parseFixture(t)
	api.Service.Groups = api.Service.Groups[:1]
	api.Service.Groups[0].Routes[1].Method = "delete"

	dir := t.TempDir()
	writeModuleFile(t, dir)
	if err := gen(api, dir); err != nil {
		t.Fatalf("gen(): %v", err)
	}
	generated, err := os.ReadFile(filepath.Join(dir, "client.go"))
	if err != nil {
		t.Fatalf("ReadFile(client.go): %v", err)
	}
	source := string(generated)
	start := strings.Index(source, "func (c *orderApiClient) GetOrder")
	if start < 0 {
		t.Fatal("generated DELETE method not found")
	}
	method := source[start:]
	end := strings.Index(method, "\n}\n")
	if end < 0 {
		t.Fatal("generated DELETE method is not closed")
	}
	if strings.Contains(method[:end], "SetBody") {
		t.Errorf("generated DELETE client includes a request body:\n%s", method[:end])
	}
}

func TestGenerate_MatchesDirectoryPackage(t *testing.T) {
	api := parseFixture(t)
	api.Service.Groups = api.Service.Groups[:1]
	dir := t.TempDir()
	writeModuleFile(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "existing.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(existing.go): %v", err)
	}
	if err := gen(api, dir); err != nil {
		t.Fatalf("gen(): %v", err)
	}
	generated, err := os.ReadFile(filepath.Join(dir, "client.go"))
	if err != nil {
		t.Fatalf("ReadFile(client.go): %v", err)
	}
	if !bytes.HasPrefix(generated, []byte("package app\n")) {
		t.Errorf("generated package = %q, want app", strings.SplitN(string(generated), "\n", 2)[0])
	}
}

func TestDeriveTypesPkg(t *testing.T) {
	t.Run("reads module path", func(t *testing.T) {
		dir := t.TempDir()
		writeModuleFile(t, dir)
		got, err := deriveTypesPkg(dir)
		if err != nil {
			t.Fatalf("deriveTypesPkg(): %v", err)
		}
		if got != "example.com/svc/internal/types" {
			t.Errorf("deriveTypesPkg() = %q, want example.com/svc/internal/types", got)
		}
	})

	t.Run("accepts module comments", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/svc // module path\n\ngo 1.25\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(go.mod): %v", err)
		}
		got, err := deriveTypesPkg(dir)
		if err != nil {
			t.Fatalf("deriveTypesPkg(): %v", err)
		}
		if got != "example.com/svc/internal/types" {
			t.Errorf("deriveTypesPkg() = %q, want example.com/svc/internal/types", got)
		}
	})

	t.Run("reports missing go.mod", func(t *testing.T) {
		_, err := deriveTypesPkg(t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "jinx-goctl: cannot derive types package from") {
			t.Fatalf("deriveTypesPkg() error = %v, want cannot derive types package error", err)
		}
	})
}

func TestGeneratedClientBuilds(t *testing.T) {
	api := parseFixture(t)
	api.Service.Groups = api.Service.Groups[:1]
	dir := t.TempDir()
	writeBuildModule(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "internal", "types"), 0o755); err != nil {
		t.Fatalf("MkdirAll(types): %v", err)
	}
	typesSource := `package types

type GetOrderReq struct{}
type GetOrderResp struct{}
type CreateOrderReq struct{}
type CreateOrderResp struct{}
`
	if err := os.WriteFile(filepath.Join(dir, "internal", "types", "types.go"), []byte(typesSource), 0o600); err != nil {
		t.Fatalf("WriteFile(types.go): %v", err)
	}
	if err := gen(api, dir); err != nil {
		t.Fatalf("gen(): %v", err)
	}

	commands := [][]string{{"mod", "tidy"}, {"build", "./..."}, {"vet", "./..."}}
	for _, args := range commands {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
}

func parseFixture(t *testing.T) *spec.ApiSpec {
	t.Helper()
	api, err := parser.Parse(filepath.Join("testdata", "order.api"))
	if err != nil {
		t.Fatalf("parser.Parse(): %v", err)
	}
	return api
}

func writeModuleFile(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/svc\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod): %v", err)
	}
}

func writeBuildModule(t *testing.T, dir string) {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	coreDir, err := filepath.Abs(filepath.Join(filepath.Dir(sourceFile), ".."))
	if err != nil {
		t.Fatalf("filepath.Abs(): %v", err)
	}
	content := fmt.Sprintf(
		"module example.com/svc\n\ngo 1.25\n\nrequire github.com/anyoptional/jinx v0.0.0\n\nreplace github.com/anyoptional/jinx => %s\n",
		filepath.ToSlash(coreDir),
	)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod): %v", err)
	}
	sum, err := os.ReadFile(filepath.Join(coreDir, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(core go.sum): %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0o600); err != nil {
		t.Fatalf("WriteFile(go.sum): %v", err)
	}
}

func normalizeLineEndings(content []byte) []byte {
	return bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
}
