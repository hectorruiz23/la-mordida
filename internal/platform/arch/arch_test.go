// Package arch holds the import limits a change has to respect.
package arch

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate arch_test.go")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root %q does not contain go.mod: %v", root, err)
	}
	return root
}

func domainPackages(t *testing.T) []string {
	t.Helper()

	directories, err := filepath.Glob(filepath.Join(repoRoot(t), "internal", "*", "domain"))
	if err != nil {
		t.Fatalf("glob domain packages: %v", err)
	}
	if len(directories) == 0 {
		t.Fatal("no domain packages found under internal/*/domain")
	}
	return directories
}

func packageImports(t *testing.T, directory string) []string {
	t.Helper()

	var imports []string
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read %s: %v", directory, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file := filepath.Join(directory, entry.Name())
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import in %s: %v", file, err)
			}
			imports = append(imports, importPath)
		}
	}
	return imports
}

func standardLibrary(importPath string) bool {
	first := strings.Split(importPath, "/")[0]
	return !strings.Contains(first, ".")
}

const modulePath = "github.com/hectorruiz23/la-mordida"
const kernelImport = modulePath + "/internal/kernel"

func TestDomainImportsOnlyStandardLibraryAndKernel(t *testing.T) {
	for _, directory := range domainPackages(t) {
		for _, importPath := range packageImports(t, directory) {
			if standardLibrary(importPath) || importPath == kernelImport {
				continue
			}
			t.Errorf("%s imports %s, want only the standard library and the kernel", directory, importPath)
		}
	}
}

func TestDomainDoesNotDependOnPLACSPOrSQLite(t *testing.T) {
	forbidden := []string{"placsp", "sqlite", "database/sql", "modernc.org", "net/http"}

	for _, directory := range domainPackages(t) {
		for _, importPath := range packageImports(t, directory) {
			lower := strings.ToLower(importPath)
			for _, needle := range forbidden {
				if strings.Contains(lower, needle) {
					t.Errorf("%s imports %s: domain may not depend on %s", directory, importPath, needle)
				}
			}
		}
	}
}

func TestCapabilitySlicesHaveDomainAppAndAdapters(t *testing.T) {
	internalRoot := filepath.Join(repoRoot(t), "internal")
	entries, err := os.ReadDir(internalRoot)
	if err != nil {
		t.Fatalf("read %s: %v", internalRoot, err)
	}

	reserved := map[string]bool{"kernel": true, "platform": true}
	for _, entry := range entries {
		if !entry.IsDir() || reserved[entry.Name()] || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		for _, required := range []string{"domain", "app", "adapters"} {
			directory := filepath.Join(internalRoot, entry.Name(), required)
			info, err := os.Stat(directory)
			if err != nil || !info.IsDir() {
				t.Errorf("slice %s has no %s directory", entry.Name(), required)
			}
		}
	}
}

func TestCapabilityPackagesStayInTheirSlice(t *testing.T) {
	internalRoot := filepath.Join(repoRoot(t), "internal")
	entries, err := os.ReadDir(internalRoot)
	if err != nil {
		t.Fatalf("read %s: %v", internalRoot, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "kernel" || entry.Name() == "platform" {
			continue
		}
		domain := filepath.Join(internalRoot, entry.Name(), "domain")
		if _, err := os.Stat(domain); err != nil {
			continue
		}
		for _, importPath := range packageImports(t, domain) {
			if strings.HasPrefix(importPath, modulePath+"/internal/") && importPath != kernelImport {
				if !strings.HasPrefix(importPath, modulePath+"/internal/"+entry.Name()) {
					t.Errorf("%s/domain imports %s, want no other capability", entry.Name(), importPath)
				}
			}
		}
	}
}
