package generate

import (
	"bytes"
	"embed"
	"go/format"
	"io"
	"text/template"
)

//go:embed *.tmpl
var templates embed.FS

// renderer renders Go declarations and templates into one target package using
// an isolated importScope.
type renderer struct {
	pkgName       string
	imports       *importScope
	templateCache map[string]*template.Template
}

func newRenderer(pkgName, pkgPath string) *renderer {
	return &renderer{
		pkgName:       pkgName,
		imports:       newImportScope(pkgPath),
		templateCache: map[string]*template.Template{},
	}
}

func (r *renderer) typeRef(typ goType) string {
	return typ.renderRef(r.imports)
}

// render executes the given template with this renderer's import-aware funcs.
func (r *renderer) render(tmplRelFilename string, w io.Writer, data any) error {
	tmpl := r.templateCache[tmplRelFilename]
	if tmpl == nil {
		funcMap := template.FuncMap{
			"ref":     r.imports.ref,
			"typeRef": r.typeRef,
		}
		var err error
		tmpl, err = template.New(tmplRelFilename).Funcs(funcMap).ParseFS(templates, tmplRelFilename)
		if err != nil {
			return errorf(nil, "could not load template %v: %v", tmplRelFilename, err)
		}
		r.templateCache[tmplRelFilename] = tmpl
	}
	err := tmpl.Execute(w, data)
	if err != nil {
		return errorf(nil, "could not render template: %v", err)
	}
	return nil
}

// formatGoFile prepends the package header and collected imports to body, then
// formats the complete source once.
func (r *renderer) formatGoFile(
	headerComment string,
	opLabel string,
	body []byte,
) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(headerComment)
	buf.WriteString("\n\npackage ")
	buf.WriteString(r.pkgName)
	buf.WriteString("\n\n")
	buf.WriteString(r.imports.declaration())
	buf.Write(body)

	unformatted := buf.Bytes()
	formatted, err := format.Source(unformatted)
	if err != nil {
		return nil, goSourceError("gofmt"+opLabel, unformatted, err)
	}
	return formatted, nil
}
