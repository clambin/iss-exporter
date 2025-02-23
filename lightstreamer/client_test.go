package lightstreamer

import (
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestClient(t *testing.T) {
	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ts := httptest.NewServer(NewServer("mySet", "myCID", nil, l))
	t.Cleanup(ts.Close)

	c := NewClient(
		WithLogger(l),
		WithServerURL(ts.URL),
		WithAdapterSet("mySet"),
		WithCID("myCID"),
	)
	ctx := t.Context()
	conn, err := c.Connect(ctx)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	if err = conn.WaitOnConnection(ctx, 5*time.Second); err != nil {
		t.Fatalf("timeout waiting on session: %v", err)
	}
}

func TestClient_ISS(t *testing.T) {
	//t.Skip()

	ctx := t.Context()
	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := NewClient(
		WithLogger(l),
		WithAdapterSet("ISSLIVE"),
		WithContentLength(100),
	)
	conn, err := c.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err = conn.WaitOnConnection(ctx, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	for _, group := range []string{"TIME_000001", "NODE3000009"} {
		if err = conn.Subscribe(ctx, "DEFAULT", group, []string{"Value"}, func(item int, values Values) {
			l.Info(group, "value", values)
		}); err != nil {
			t.Fatal(err)
		}
	}

	<-ctx.Done()
}
