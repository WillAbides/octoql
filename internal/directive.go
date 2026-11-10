package internal

import (
	_ "embed"
)

//go:embed octoqlgen-directive.graphql
var DirectiveFileContents string

// DirectiveFilename is the companion schema file name for octoqlgen's directive SDL.
const DirectiveFilename = "octoqlgen-directive.graphql"
