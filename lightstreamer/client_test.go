package lightstreamer

import (
	"log/slog"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestClient(t *testing.T) {
	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ts := httptest.NewServer(NewServer("mySet", "myCID", nil, l))
	t.Cleanup(ts.Close)

	c := NewClient("mySet", "myCID", l)
	c.ServerURL = ts.URL
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
	t.Skip()

	ctx := t.Context()
	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := NewClient("ISSLIVE", "mgQkwtwdysogQz2BJ4Ji%20kOj2Bg", l)
	conn, err := c.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err = conn.WaitOnConnection(ctx, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	if err = conn.Subscribe(ctx, "DEFAULT", "TIME_000001", []string{"Value"}, func(item int, values Values) {
		msec, _ := strconv.ParseInt(values[0], 10, 64)
		l.Info("update", "item", item, "time", time.Date(2025, time.January, 0, 0, 0, 0, 0, time.UTC).Add(time.Duration(msec)*time.Millisecond))
	}); err != nil {
		t.Fatal(err)
	}

	if err = conn.Subscribe(ctx, "DEFAULT", "NODE3000011", []string{"Value"}, func(item int, values Values) {
		l.Info("O2 production rate", "value", values)
	}); err != nil {
		t.Fatal(err)
	}

	<-ctx.Done()
}
