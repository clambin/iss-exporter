package lightstreamer

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServer_Connect(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		args   url.Values
		parms  url.Values
		want   int
	}{
		{
			name:   "success",
			method: http.MethodPost,
			path:   "/create_session.txt",
			args:   url.Values{"LS_protocol": []string{"TLCP-2.1.0"}},
			parms: url.Values{
				"LS_adapter_set": []string{"set"},
				"LS_cid":         []string{"cid"},
			},
			want: http.StatusOK,
		},
		{
			name:   "invalid method",
			method: http.MethodGet,
			path:   "/create_session.txt",
			args:   url.Values{"LS_protocol": []string{"TLCP-2.1.0"}},
			parms: url.Values{
				"LS_adapter_set": []string{"set"},
				"LS_cid":         []string{"cid"},
			},
			want: http.StatusMethodNotAllowed,
		},
		{
			name:   "invalid path",
			method: http.MethodPost,
			path:   "/create_session",
			args:   url.Values{"LS_protocol": []string{"TLCP-2.1.0"}},
			parms: url.Values{
				"LS_adapter_set": []string{"set"},
				"LS_cid":         []string{"cid"},
			},
			want: http.StatusNotFound,
		},
		{
			name:   "invalid protocol",
			method: http.MethodPost,
			path:   "/create_session.txt",
			args:   url.Values{"LS_protocol": []string{"TLCP-2.2.0"}},
			parms: url.Values{
				"LS_adapter_set": []string{"set"},
				"LS_cid":         []string{"cid"},
			},
			want: http.StatusBadRequest,
		},
		{
			name:   "missing parameters",
			method: http.MethodPost,
			path:   "/create_session.txt",
			args:   url.Values{"LS_protocol": []string{"TLCP-2.1.0"}},
			parms:  url.Values{},
			want:   http.StatusBadRequest,
		},
		{
			name:   "invalid set",
			method: http.MethodPost,
			path:   "/create_session.txt",
			args:   url.Values{"LS_protocol": []string{"TLCP-2.1.0"}},
			parms: url.Values{
				"LS_adapter_set": []string{"bad-set"},
				"LS_cid":         []string{"cid"},
			},
			want: http.StatusBadRequest,
		},
		{
			name:   "invalid cid",
			method: http.MethodPost,
			path:   "/create_session.txt",
			args:   url.Values{"LS_protocol": []string{"TLCP-2.1.0"}},
			parms: url.Values{
				"LS_adapter_set": []string{"set"},
				"LS_cid":         []string{"bad-cid"},
			},
			want: http.StatusBadRequest,
		},
	}

	l := slog.New(slog.DiscardHandler)
	s := NewServer("set", "cid", nil, l)
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			req, _ := http.NewRequestWithContext(ctx, tt.method, ts.URL+tt.path+"?"+tt.args.Encode(), strings.NewReader(tt.parms.Encode()))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			if got := resp.StatusCode; got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
			cancel()
		})
	}
}

func TestServer_Subscribe(t *testing.T) {
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

	//l := slog.New(slog.DiscardHandler)
	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := NewServer("set", "cid", map[string]AdapterSet{"DEFAULT": {"1": &a}}, l)
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			c := NewClient(
				WithLogger(l),
				WithServerURL(ts.URL),
				WithAdapterSet("set"),
				WithCID("cid"),
			)
			conn, err := c.Connect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = conn.waitOnConnection(ctx, 5*time.Second); err != nil {
				t.Fatal(err)
			}

			var rcvd atomic.Bool
			err = conn.Subscribe(ctx, tt.adapter, tt.group, []string{"Value"}, func(item int, values Values) {
				rcvd.Store(true)
			})
			if tt.wantErr != (err != nil) {
				t.Errorf("got %v, wantErr %v", err, tt.wantErr)
			}

			if err != nil {
				return
			}

			var count int
			for !rcvd.Load() && count < 20 {
				time.Sleep(100 * time.Millisecond)
				count++
			}
			if !rcvd.Load() {
				t.Errorf("no update received after %d attempts", count)
			}
		})
	}
}

////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

func TestAdapter_Run(t *testing.T) {
	var adapter timedAdapter
	go adapter.Run(t.Context(), 200*time.Millisecond)

	ch := make(chan AdapterUpdate)
	_, _, _ = adapter.Subscribe(ch, 1, "", "")

	for want := range 5 {
		got := <-ch

		if got.SubscriptionID != 1 {
			t.Errorf("got %d, want 1", got.SubscriptionID)
		}

		if got.Values.String() != strconv.Itoa(want+1) {
			t.Errorf("got %s, want %s", got.Values.String(), "1")
		}
	}
}

var _ Adapter = &timedAdapter{}

type timedAdapter struct {
	lock          sync.RWMutex
	subscriptions map[int]chan<- AdapterUpdate
}

func (t *timedAdapter) Subscribe(ch chan<- AdapterUpdate, subId int, _ string, _ string) (int, int, error) {
	t.lock.Lock()
	defer t.lock.Unlock()
	if t.subscriptions == nil {
		t.subscriptions = make(map[int]chan<- AdapterUpdate)
	}
	t.subscriptions[subId] = ch
	return 1, 1, nil
}

func (t *timedAdapter) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(cmp.Or(interval, time.Second))
	defer ticker.Stop()

	var value int
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			value++
			t.publish(Values{strconv.Itoa(value)})
		}
	}
}

func (t *timedAdapter) publish(values Values) {
	t.lock.RLock()
	defer t.lock.RUnlock()
	for id, ch := range t.subscriptions {
		ch <- AdapterUpdate{SubscriptionID: id, Item: 1, Values: values}
	}
}

func (t *timedAdapter) String() string {
	return "timedAdapter"
}
