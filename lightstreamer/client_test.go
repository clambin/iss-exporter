package lightstreamer

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
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
		WithHTTPClient(ts.Client()),
		WithAdapterSet("mySet"),
		WithCID("myCID"),
	)
	ctx := t.Context()
	conn, err := c.Connect(ctx)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	if err = conn.waitOnConnection(ctx, 5*time.Second); err != nil {
		t.Fatalf("timeout waiting on session: %v", err)
	}
}

func TestClient_Timeout(t *testing.T) {
	l := slog.New(slog.DiscardHandler)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(time.Minute):
		}
	}))
	t.Cleanup(ts.Close)

	c := NewClient(
		WithLogger(l),
		WithServerURL(ts.URL),
		WithHTTPClient(ts.Client()),
		WithConnectTimeout(time.Second),
	)
	if _, err := c.Connect(t.Context()); err == nil {
		t.Error("expected timeout error")
	}
}

func TestClient_Rebind(t *testing.T) {
	l := slog.New(slog.DiscardHandler)
	var rebound atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		switch r.URL.Path {
		case "/create_session.txt":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("CONOK,mySessionID,50000,5000,*\r\n"))
			_, _ = w.Write([]byte("LOOP,0\r\n"))
			w.(http.Flusher).Flush()
		case "/bind_session.txt":
			if !bytes.Equal(body, []byte("LS_session=mySessionID")) {
				http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			rebound.Store(true)
		default:
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
	}))
	t.Cleanup(ts.Close)
	c := NewClient(
		WithLogger(l),
		WithServerURL(ts.URL),
	)
	_, err := c.Connect(t.Context())
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	start := time.Now()
	for !rebound.Load() {
		time.Sleep(100 * time.Millisecond)
		if time.Since(start) > 5*time.Second {
			t.Errorf("timeout waiting for rebind")
			break
		}
	}
}

func TestClient_ISS(t *testing.T) {
	t.Skip()

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

	if err = conn.waitOnConnection(ctx, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	for _, group := range []string{"USLAB000058"} {
		if err = conn.Subscribe(ctx, "DEFAULT", group, []string{"Value"}, func(item int, values Values) {
			l.Info(group, "value", values)
		}); err != nil {
			t.Fatal(err)
		}
	}

	<-ctx.Done()
}
