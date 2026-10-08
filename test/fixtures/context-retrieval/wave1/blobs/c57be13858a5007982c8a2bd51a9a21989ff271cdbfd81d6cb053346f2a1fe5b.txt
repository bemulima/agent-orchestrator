package architecture_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/example/user-service/"

func TestInwardPackagesDoNotImportTransportOrInfrastructure(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	rules := map[string][]string{
		"internal/domain":  {modulePath + "internal/transport", modulePath + "internal/infrastructure", "gorm.io"},
		"internal/usecase": {modulePath + "internal/transport", modulePath + "internal/infrastructure", "gorm.io"},
		"internal/port":    {modulePath + "internal/transport", modulePath + "internal/infrastructure", "gorm.io"},
	}
	for relativeDir, forbidden := range rules {
		dir := filepath.Join(root, relativeDir)
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if parseErr != nil {
				return parseErr
			}
			for _, imported := range file.Imports {
				importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
				if unquoteErr != nil {
					return unquoteErr
				}
				for _, prefix := range forbidden {
					if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
						t.Errorf("%s imports forbidden package %s", path, importPath)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", relativeDir, err)
		}
	}
}
