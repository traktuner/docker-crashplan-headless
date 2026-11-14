package engine

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Audit (Low): a stalled result-stream consumer may cost the reader one
// bounded wait, not stall every other subscriber, and its stream loses
// continuity (epoch) so it fails closed.
func TestStalledSearchSubscriberDoesNotStallOtherEvents(t *testing.T) {
	ready := make(chan *websocket.Conn, 1)
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ready <- conn
		for {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bus, err := c.OpenEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseEvents()
	_, closeSearch, err := bus.Subscribe("SearchFileVersionsResultMessage") // never read
	if err != nil {
		t.Fatal(err)
	}
	defer closeSearch()
	alerts, closeAlerts, err := bus.Subscribe("AlertListMessage")
	if err != nil {
		t.Fatal(err)
	}
	defer closeAlerts()
	epoch := bus.Epoch()
	conn := <-ready
	for i := 0; i < criticalBuffer+40; i++ {
		if err := wsjson.Write(ctx, conn, map[string]any{"SearchFileVersionsResultMessage": map[string]any{"done": false}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := wsjson.Write(ctx, conn, map[string]any{"AlertListMessage": map[string]any{"alerts": []any{}}}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	select {
	case <-alerts:
	case <-time.After(criticalWait + 3*time.Second):
		t.Fatal("a stalled search subscriber stalled other events")
	}
	if waited := time.Since(started); waited > criticalWait+2*time.Second {
		t.Fatal("more than one bounded wait", waited)
	}
	if bus.Epoch() == epoch {
		t.Fatal("dropped events did not end the stream's continuity")
	}
}
