// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package clientgetter

import (
	"context"

	"github.com/willabides/octoql"
)

// Context is a custom context type carrying an octoql client.
type Context interface {
	context.Context
	OctoqlClient() *octoql.Client
}

// GetClient returns the octoql client carried by ctx.
func GetClient(ctx Context) (*octoql.Client, error) {
	return ctx.OctoqlClient(), nil
}
