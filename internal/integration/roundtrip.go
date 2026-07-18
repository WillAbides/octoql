package integration

// Client helpers for integration tests.

import (
	"net/http"

	"github.com/willabides/octoql"
)

func newRoundtripClients(endpoint string) []*octoql.Client {
	return []*octoql.Client{newRoundtripClient(endpoint)}
}

func newRoundtripClient(endpoint string) *octoql.Client {
	return octoql.NewClient(endpoint, http.DefaultClient)
}
