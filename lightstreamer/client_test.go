package lightstreamer

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientSession_Connect(t *testing.T) {
	//l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		for _, resp := range []string{
			"CONOK,1,5000,50000,*",
			"SYNC,0",
			"END,0,no error",
		} {
			_, _ = w.Write([]byte(resp + "\n"))
			w.(http.Flusher).Flush()
		}
		//<-r.Context().Done()
	}))
	t.Cleanup(ts.Close)

	c := NewClientSession(
		//WithLogger(l),
		WithServerURL(ts.URL),
		WithHTTPClient(ts.Client()),
		WithAdapterSet("mySet"),
		WithCID("myCID"),
	)
	if err := c.ConnectWithSession(t.Context(), time.Second); err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	t.Cleanup(c.Disconnect)

	if got := c.sessionID.Load().(string); got != "1" {
		t.Errorf("got session ID %q, expected 1", got)
	}
}

func TestClientSession_Connect_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(ts.Close)

	c := NewClientSession(
		WithServerURL(ts.URL),
	)
	if err := c.ConnectWithSession(t.Context(), time.Second); err == nil {
		t.Error("expected timeout error")
	}
}

func TestClientSession_Rebind(t *testing.T) {
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
	c := NewClientSession(
		WithServerURL(ts.URL),
	)
	if err := c.Connect(t.Context()); err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	t.Cleanup(c.Disconnect)

	start := time.Now()
	for !rebound.Load() {
		if time.Since(start) > 5*time.Second {
			t.Errorf("timeout waiting for rebind")
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestClientSession_Subscribe(t *testing.T) {
	tests := []struct {
		name    string
		adapter string
		group   string
		wantErr bool
	}{
		{
			name:    "success",
			adapter: "DEFAULT",
			group:   "1",
			wantErr: false,
		},
		{
			name:    "invalid adapter",
			adapter: "invalid",
			group:   "1",
			wantErr: true,
		},
		{
			name:    "invalid group",
			adapter: "DEFAULT",
			group:   "0",
			wantErr: true,
		},
	}

	var a timedAdapter
	go a.Run(t.Context(), 500*time.Millisecond)

	l := slog.New(slog.DiscardHandler)
	//l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := NewServer("set", "cid", map[string]AdapterSet{"DEFAULT": {"1": &a}}, l)
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientSession := NewClientSession(
				WithLogger(l),
				WithServerURL(ts.URL),
				WithAdapterSet("set"),
				WithCID("cid"),
			)
			if err := clientSession.ConnectWithSession(t.Context(), time.Second); err != nil {
				t.Fatalf("failed to connect: %v", err)
			}
			t.Cleanup(clientSession.Disconnect)

			var rcvd atomic.Int32
			err := clientSession.Subscribe(t.Context(), tt.adapter, tt.group, []string{"Value"}, 0, func(item int, values Values) {
				rcvd.Add(1)
			})
			if tt.wantErr != (err != nil) {
				t.Errorf("got %v, wantErr %v", err, tt.wantErr)
			}

			if err != nil {
				return
			}

			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()

			for rcvd.Load() == 0 {
				select {
				case <-ctx.Done():
					t.Fatal("timeout waiting for updates")
				case <-time.After(100 * time.Millisecond):
				}
			}
		})
	}
}

func TestClientSession_Subscribe_NoSession(t *testing.T) {
	c := NewClientSession()
	if err := c.Subscribe(t.Context(), "", "", nil, 0, nil); err == nil {
		t.Error("expected error")
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
