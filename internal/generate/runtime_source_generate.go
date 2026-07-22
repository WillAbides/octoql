//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := filepath.Join("..", "..")
	filenames := []string{
		"runtime_guard.go",
		"errors.go",
		"rate_limit.go",
		"client.go",
	}

	var output bytes.Buffer
	for _, filename := range filenames {
		content, err := declarations(filepath.Join(root, filename))
		if err != nil {
			panic(err)
		}
		output.Write(content)
		output.WriteString("\n\n")
	}

	replacer := strings.NewReplacer(
		"NoUnmarshalJSON", "noUnmarshalJSON",
		"NoMarshalJSON", "noMarshalJSON",
		"Payload", "payload",
		"Execute", "execute",
	)
	err := os.WriteFile(
		"runtime.go.tmpl",
		[]byte(replacer.Replace(output.String())),
		0o600,
	)
	if err != nil {
		panic(err)
	}
}

func declarations(filename string) ([]byte, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filename, err)
	}
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, filename, content, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}

	for _, declaration := range file.Decls {
		generic, isImport := declaration.(*ast.GenDecl)
		if isImport && generic.Tok == token.IMPORT {
			continue
		}
		position := declaration.Pos()
		if generic != nil && generic.Doc != nil {
			position = generic.Doc.Pos()
		}
		offset := fileSet.Position(position).Offset
		return bytes.TrimSpace(content[offset:]), nil
	}
	return nil, fmt.Errorf("finding declarations in %s", filename)
}
