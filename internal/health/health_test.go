package health

import (
	"github.com/clambin/iss-exporter/lightstreamer"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	s := lightstreamer.NewClientSession()
	p := Handler(s)

	req_, _ := http.NewRequest("GET", "/", nil)
	resp := httptest.NewRecorder()
	p.ServeHTTP(resp, req_)
	if resp.Code != http.StatusServiceUnavailable {
		t.Errorf("got %v want %v", resp.Code, http.StatusServiceUnavailable)
	}

	s.Connections.Add(1)
	req_, _ = http.NewRequest("GET", "/", nil)
	resp = httptest.NewRecorder()
	p.ServeHTTP(resp, req_)
	if resp.Code != http.StatusOK {
		t.Errorf("got %v want %v", resp.Code, http.StatusOK)
	}

	s.Connections.Add(-1)
	req_, _ = http.NewRequest("GET", "/", nil)
	resp = httptest.NewRecorder()
	p.ServeHTTP(resp, req_)
	if resp.Code != http.StatusServiceUnavailable {
		t.Errorf("got %v want %v", resp.Code, http.StatusServiceUnavailable)
	}
}
