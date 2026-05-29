package collector_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/dobbo-ca/lynceus/internal/collector"
	lynceusv1 "github.com/dobbo-ca/lynceus/internal/proto/lynceus/v1"
)

func TestShipper_sendsSnapshotAndCarriesBearerToken(t *testing.T) {
	type received struct {
		auth string
		snap *lynceusv1.Snapshot
		err  error
	}
	got := make(chan received, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			got <- received{err: err}
			return
		}
		defer conn.CloseNow()

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		_, data, err := conn.Read(ctx)
		if err != nil {
			got <- received{err: err}
			return
		}
		var snap lynceusv1.Snapshot
		if err := proto.Unmarshal(data, &snap); err != nil {
			got <- received{err: err}
			return
		}
		got <- received{auth: auth, snap: &snap}
		// Send a close frame so the client's graceful Close handshake
		// succeeds instead of seeing EOF on the read.
		_ = conn.Close(websocket.StatusNormalClosure, "ok")
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	s := collector.NewShipper(wsURL, "dev-token-abc")

	snap := &lynceusv1.Snapshot{
		ServerId:        "srv-1",
		CollectedAtUnix: time.Now().Unix(),
		QueryStats: []*lynceusv1.QueryStat{{
			Fingerprint:     "fp-1",
			NormalizedQuery: "SELECT $1",
			Calls:           42,
			TotalTimeMs:     12.5,
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Send(ctx, snap); err != nil {
		t.Fatalf("send: %v", err)
	}

	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("server-side err: %v", r.err)
		}
		if r.auth != "Bearer dev-token-abc" {
			t.Errorf("auth header = %q, want %q", r.auth, "Bearer dev-token-abc")
		}
		if r.snap.ServerId != "srv-1" {
			t.Errorf("server_id = %q, want srv-1", r.snap.ServerId)
		}
		if len(r.snap.QueryStats) != 1 || r.snap.QueryStats[0].Fingerprint != "fp-1" {
			t.Errorf("query_stats not roundtripped: %+v", r.snap.QueryStats)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for server to receive snapshot")
	}
}
