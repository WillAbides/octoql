package clientgetter

import (
	"context"

	"github.com/willabides/octoql"
)

// customContext is a custom context type carrying an octoql client.
type customContext interface {
	context.Context
	OctoqlClient() (*octoql.Client, error)
}

// getClient returns the octoql client carried by ctx.
func getClient(ctx customContext) (*octoql.Client, error) {
	return ctx.OctoqlClient()
}
