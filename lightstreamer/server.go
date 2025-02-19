package lightstreamer

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"
)

type Server struct {
	set string
	cid string
	http.Handler
	logger *slog.Logger
	connID atomic.Uint64
}

const (
	keepAlivePeriodMilliSeconds = 5000
)

func NewServer(set string, cid string, logger *slog.Logger) *Server {
	s := Server{
		set:    set,
		cid:    cid,
		logger: logger,
	}
	m := http.NewServeMux()
	m.HandleFunc("POST /create_session.txt", s.session)
	m.HandleFunc("POST /control.txt", s.control)
	s.Handler = m
	return &s
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("session called", "query", r.URL.Query())
	if err := s.parseSessionRequest(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check that the session is flushable
	_, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	sess := session{
		connID:  s.connID.Add(1),
		created: time.Now(),
		server:  s,
	}
	sess.serve(w, r)
}

func (s *Server) parseSessionRequest(r *http.Request) error {
	if protocol := r.URL.Query().Get("LS_protocol"); protocol != "TLCP-2.1.0" {
		return fmt.Errorf("unsupported protocol: %s", protocol)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("io.ReadAll: %w", err)
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "application/x-www-form-urlencoded" {
		return fmt.Errorf("unsupported content type: %s", contentType)
	}
	req, err := url.ParseQuery(string(body))
	if err != nil {
		return fmt.Errorf("url.ParseQuery: %w", err)
	}
	if set := req.Get("LS_adapter_set"); set != s.set {
		return fmt.Errorf("invalid adapter set: %s", set)
	}
	if cid := req.Get("LS_cid"); cid != s.cid {
		return fmt.Errorf("invalid cid: %s", cid)
	}
	return nil
}

func (s *Server) writeLine(w http.ResponseWriter, line string) error {
	_, err := io.WriteString(w, line+"\n")
	//w.(http.Flusher).Flush()
	return err
}

func (s *Server) control(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("control called", "query", r.URL.Query())

}

func (s *Server) parseControlRequest(r *http.Request) error {
	if protocol := r.URL.Query().Get("LS_protocol"); protocol != "TLCP-2.0.0" {
		return fmt.Errorf("unsupported protocol: %s", protocol)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("io.ReadAll: %w", err)
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "application/x-www-form-urlencoded" {
		return fmt.Errorf("unsupported content type: %s", contentType)
	}
	req, err := url.ParseQuery(string(body))
	if err != nil {
		return fmt.Errorf("url.ParseQuery: %w", err)
	}
	if set := req.Get("LS_adapter_set"); set != s.set {
		return fmt.Errorf("invalid adapter set: %s", set)
	}
	if cid := req.Get("LS_cid"); cid != s.cid {
		return fmt.Errorf("invalid cid: %s", cid)
	}
	return nil
}

////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

type session struct {
	connID    uint64
	created   time.Time
	lastWrite time.Time
	server    *Server
}

func (s *session) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/enriched; charset=UTF-8")
	w.Header().Add("Cache-Control", "no-store")
	w.Header().Add("Cache-Control", "no-transform")
	w.Header().Add("Cache-Control", "no-cache")
	w.Header().Add("Pragma", "no-cache")
	w.Header().Set("Transfer-Encoding", "chunked")

	w.WriteHeader(http.StatusOK)

	_ = s.writeLine(w, fmt.Sprintf("CONOK,%d,5000,%d,*", s.connID, keepAlivePeriodMilliSeconds))
	_ = s.writeLine(w, "SERVNAME,fake server")
	_ = s.writeLine(w, "CONS,unlimited")

	syncTicker := time.NewTicker(time.Second)
	defer syncTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-syncTicker.C:
			if time.Since(s.lastWrite) > keepAlivePeriodMilliSeconds*time.Millisecond {
				_ = s.writeLine(w, fmt.Sprintf("SYNC,%d", int(time.Since(s.created).Seconds())))
			}
		}
	}
}

func (s *session) writeLine(w http.ResponseWriter, line string) error {
	_, err := io.WriteString(w, line+"\n")
	w.(http.Flusher).Flush()
	s.lastWrite = time.Now()
	return err
}
