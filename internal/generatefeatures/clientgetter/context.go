package clientgetter

import (
	"context"

	"github.com/willabides/octoql"
)

// customContext is a custom context type carrying an octoql client.
type customContext interface {
	context.Context
	OctoqlClient() *octoql.Client
}

// getClient returns the octoql client carried by ctx.
//
//nolint:unparam // Generated client getters return an error.
func getClient(ctx customContext) (*octoql.Client, error) {
	return ctx.OctoqlClient(), nil
}
