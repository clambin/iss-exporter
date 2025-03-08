package lightstreamer

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient(t *testing.T) {
	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ts := httptest.NewServer(NewServer("mySet", "myCID", nil, l))
	t.Cleanup(ts.Close)

	c, err := NewClientSession(
		t.Context(),
		WithLogger(l),
		WithServerURL(ts.URL),
		WithHTTPClient(ts.Client()),
		WithAdapterSet("mySet"),
		WithCID("myCID"),
	)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	if !c.Bound() {
		t.Error("failed to connect")
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

	_, err := NewClientSession(
		t.Context(),
		WithLogger(l),
		WithServerURL(ts.URL),
		WithHTTPClient(ts.Client()),
		WithBindTimeout(time.Second),
	)
	if err == nil {
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
			_, _ = w.Write([]byte("SYNC,0\r\n"))
			_, _ = w.Write([]byte("LOOP,0\r\n"))
			_, _ = w.Write([]byte("END,0,no error\r\n"))
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
	_, err := NewClientSession(
		t.Context(),
		WithLogger(l),
		WithServerURL(ts.URL),
	)
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

func Test_lsError(t *testing.T) {
	tests := []struct {
		name     string
		response *http.Response
		want     string
	}{
		{
			name:     "lightstreamer error",
			response: &http.Response{Body: io.NopCloser(strings.NewReader("8: Configured maximum number of actively started sessions reached.\r\n"))},
			want:     "lightstreamer: 8: Configured maximum number of actively started sessions reached.",
		},
		{
			name:     "http error w/ status text",
			response: &http.Response{StatusCode: http.StatusBadRequest, Status: "missing LS_foo", Body: io.NopCloser(strings.NewReader(""))},
			want:     "http: 400 missing LS_foo",
		},
		{
			name:     "http error",
			response: &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(""))},
			want:     "http: 400 Bad Request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := lsError(tt.response); err.Error() != tt.want {
				t.Errorf("lsError() error = %v, wantErr %v", err.Error(), tt.want)
			}
		})
	}
}
