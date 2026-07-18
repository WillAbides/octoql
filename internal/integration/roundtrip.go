package integration

// Machinery for integration tests to round-trip check the JSON-marshalers and
// unmarshalers we generate.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/willabides/octoql"
	"github.com/willabides/octoql/graphql"
)

// roundtripClient retains the inherited WebSocket test wrapper. Query and
// mutation integration tests use the concrete root client directly.
type roundtripClient struct {
	wsWrapped graphql.WebSocketClient
	t         *testing.T
}

func (c *roundtripClient) Start(ctx context.Context) (errChan chan error, err error) {
	return c.wsWrapped.Start(ctx)
}

func (c *roundtripClient) Close() error {
	return c.wsWrapped.Close()
}

func (c *roundtripClient) Subscribe(req *graphql.Request, interfaceChan interface{}, forwardDataFunc graphql.ForwardDataFunction) (string, error) {
	return c.wsWrapped.Subscribe(req, interfaceChan, forwardDataFunc)
}

func (c *roundtripClient) Unsubscribe(subscriptionID string) error {
	return c.wsWrapped.Unsubscribe(subscriptionID)
}

func newRoundtripClients(endpoint string) []*octoql.Client {
	return []*octoql.Client{newRoundtripClient(endpoint)}
}

func newRoundtripClient(endpoint string) *octoql.Client {
	return octoql.NewClient(endpoint, http.DefaultClient)
}

type MyDialer struct {
	*websocket.Dialer
}

func (md *MyDialer) DialContext(ctx context.Context, urlStr string, requestHeader http.Header) (graphql.WSConn, error) {
	conn, resp, err := md.Dialer.DialContext(ctx, urlStr, requestHeader)
	resp.Body.Close()
	return graphql.WSConn(conn), err
}

func newRoundtripWebSocketClient(t *testing.T, endpoint string, opts ...graphql.WebSocketOption) graphql.WebSocketClient {
	dialer := websocket.DefaultDialer
	if !strings.HasPrefix(endpoint, "ws") {
		_, address, _ := strings.Cut(endpoint, "://")
		endpoint = "ws://" + address
	}

	return &roundtripClient{
		wsWrapped: graphql.NewClientUsingWebSocket(
			endpoint,
			&MyDialer{Dialer: dialer},
			opts...,
		),
		t: t,
	}
}
