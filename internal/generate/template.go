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

var parsedTemplates = template.Must(template.ParseFS(templates, "*.tmpl"))

// renderer renders Go declarations and templates into one target package using
// an isolated importScope.
type renderer struct {
	pkgName string
	imports *importScope
}

func newRenderer(pkgName, pkgPath string) *renderer {
	return &renderer{
		pkgName: pkgName,
		imports: newImportScope(pkgPath),
	}
}

func (r *renderer) typeRef(typ goType) string {
	return typ.renderRef(r.imports)
}

// render executes the given template.
func (r *renderer) render(tmplRelFilename string, w io.Writer, data any) error {
	err := parsedTemplates.ExecuteTemplate(w, tmplRelFilename, data)
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
