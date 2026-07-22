package integration

// Client helpers for integration tests.

import (
	"net/http"
)

func newRoundtripClients(endpoint string) []*Client {
	return []*Client{newRoundtripClient(endpoint)}
}

func newRoundtripClient(endpoint string) *Client {
	return NewClient(endpoint, http.DefaultClient)
}
