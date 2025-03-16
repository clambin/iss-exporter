package health

import (
	"github.com/clambin/iss-exporter/lightstreamer"
	"net/http"
)

func Handler(session *lightstreamer.ClientSession) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if session.Connections.Load() == 0 {
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}
