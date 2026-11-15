package generate

import (
	"bytes"
	"errors"
	"fmt"
	"go/scanner"
	"strconv"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type errorPos struct {
	filename string
	line     int
}

func (p *errorPos) String() string {
	if p.line != 0 {
		return fmt.Sprintf("%v:%v", p.filename, p.line)
	}
	return p.filename
}

type octoqlgenError struct {
	pos     *errorPos
	msg     string
	wrapped error
}

func (e *octoqlgenError) Error() string {
	if e.pos == nil {
		return e.msg
	}
	return e.pos.String() + ": " + e.msg
}

func (e *octoqlgenError) Unwrap() error {
	return e.wrapped
}

func errorf(pos *ast.Position, msg string, args ...any) error {
	// TODO: alternately accept a filename only, or maybe even a go-parser pos

	// We do all our own wrapping, because if the wrapped error already has a
	// pos, we want to extract it out and put it at the front, not in the
	// middle.

	var wrapped error
	var wrapIndex int
	for i, arg := range args {
		var ok bool
		wrapped, ok = arg.(error)
		if ok {
			wrapIndex = i
			break
		}
	}

	var wrappedOctoqlgen *octoqlgenError
	isOctoqlgen := errors.As(wrapped, &wrappedOctoqlgen)
	var wrappedGraphQL *gqlerror.Error
	isGraphQL := errors.As(wrapped, &wrappedGraphQL)
	if !isGraphQL {
		var wrappedGraphQLList gqlerror.List
		isGraphQLList := errors.As(wrapped, &wrappedGraphQLList)
		if isGraphQLList && len(wrappedGraphQLList) > 0 {
			isGraphQL = true
			wrappedGraphQL = wrappedGraphQLList[0]
		}
	}

	var errPos *errorPos
	switch {
	case pos != nil && pos.Src != nil:
		errPos = &errorPos{
			filename: pos.Src.Name,
			line:     pos.Line,
		}
	case isOctoqlgen:
		errPos = wrappedOctoqlgen.pos
	case isGraphQL:
		filename, _ := wrappedGraphQL.Extensions["file"].(string)
		if filename != "" {
			var loc gqlerror.Location
			if len(wrappedGraphQL.Locations) > 0 {
				loc = wrappedGraphQL.Locations[0]
			}
			errPos = &errorPos{
				filename: filename,
				line:     loc.Line,
			}
		}
	}

	if wrapped != nil {
		errText := wrapped.Error()
		switch {
		case isOctoqlgen:
			errText = wrappedOctoqlgen.msg
		case isGraphQL:
			errText = wrappedGraphQL.Message
		}
		args[wrapIndex] = errors.New(errText)
	}

	msg = fmt.Errorf(msg, args...).Error()

	return &octoqlgenError{
		msg:     msg,
		pos:     errPos,
		wrapped: wrapped,
	}
}

// goSourceError processes the error(s) returned by go tooling (gofmt, etc.)
// into a nice error message.
//
// In practice, such errors are octoqlgen internal errors, but it's still
// useful to format them nicely for debugging.
func goSourceError(
	failedOperation string, // e.g. "gofmt", for the error message
	source []byte,
	err error,
) error {
	var errTexts []string
	var scanErrs scanner.ErrorList
	var singleScanErr *scanner.Error

	switch {
	case errors.As(err, &scanErrs):
	case errors.As(err, &singleScanErr):
		scanErrs = scanner.ErrorList{singleScanErr}
	default:
		errTexts = []string{err.Error()}
	}

	badLines := make(map[int]bool, len(scanErrs))
	for _, scanErr := range scanErrs {
		errTexts = append(errTexts, scanErr.Error())
		badLines[scanErr.Pos.Line] = true
	}

	lines := bytes.SplitAfter(source, []byte("\n"))
	lineNoWidth := len(strconv.Itoa(len(lines)))
	for i, line := range lines {
		prefix := "  "
		if badLines[i+1] {
			prefix = "> "
		}
		lineNo := strconv.Itoa(i + 1)
		padding := strings.Repeat(" ", lineNoWidth-len(lineNo))
		lines[i] = []byte(fmt.Sprintf("%s%s%s | %s", prefix, padding, lineNo, line))
	}

	return errorf(nil,
		"octoqlgen internal error: failed to %s code:\n\t%s---source code---\n%s",
		failedOperation, strings.Join(errTexts, "\n\t"), bytes.Join(lines, nil))
}
