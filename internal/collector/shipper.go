package collector

import (
	"context"
	"fmt"
	"net/http"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

// Shipper sends Snapshot messages to the ingestion server over a
// websocket. Each Send opens its own short-lived connection — the
// long-lived streaming connection (with reconnection / backoff) is a
// later refinement.
type Shipper struct {
	url   string
	token string
}

// NewShipper returns a Shipper that will dial url with the given
// bearer token. The URL must be a `ws://` or `wss://` endpoint.
func NewShipper(url, token string) *Shipper { return &Shipper{url: url, token: token} }

// Send marshals snap as protobuf and writes it as a single binary
// websocket message.
func (s *Shipper) Send(ctx context.Context, snap *lynceusv1.Snapshot) error {
	data, err := proto.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	headers := http.Header{}
	if s.token != "" {
		headers.Set("Authorization", "Bearer "+s.token)
	}
	conn, _, err := websocket.Dial(ctx, s.url, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		return fmt.Errorf("dial %s: %w", s.url, err)
	}
	// Use a deferred error close in case Write panics or errors.
	defer conn.CloseNow()

	if err := conn.Write(ctx, websocket.MessageBinary, data); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return conn.Close(websocket.StatusNormalClosure, "")
}
