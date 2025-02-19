package lightstreamer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	serverProtocol              = "TLCP-2.1.0"
	keepAlivePeriodMilliSeconds = 5000
)

type Server struct {
	http.Handler
	AdapterSets map[string]AdapterSet
	sessions    map[string]*session
	logger      *slog.Logger
	set         string
	cid         string
	sessionID   int
	lock        sync.Mutex
}

type AdapterSet map[string]Adapter

type Adapter interface {
	Subscribe(ch chan<- AdapterUpdate, subId int, mode string, schema string) (int, int, error)
	fmt.Stringer
}

type AdapterUpdate struct {
	Values         Values
	SubscriptionID int
	Item           int
}

func NewServer(set string, cid string, adapterSets map[string]AdapterSet, logger *slog.Logger) *Server {
	s := Server{
		AdapterSets: adapterSets,
		set:         set,
		cid:         cid,
		sessions:    make(map[string]*session),
		logger:      logger,
	}
	m := http.NewServeMux()
	m.HandleFunc("POST /create_session.txt", s.session)
	m.HandleFunc("POST /control.txt", s.control)
	s.Handler = withProtocol(serverProtocol)(m)
	return &s
}

func withProtocol(want string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if protocol := r.URL.Query().Get("LS_protocol"); protocol != want {
				http.Error(w, "only supports "+want, http.StatusBadRequest)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	// Check that the session is flushable
	if _, ok := w.(http.Flusher); !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}
	var cmdCount int
	for cmd, err := range readSessionCommands(r.Body) {
		if err != nil {
			http.Error(w, "failed to read request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if cmd.AdapterSet != s.set {
			http.Error(w, "invalid adapter set", http.StatusBadRequest)
			return
		}
		if cmd.CID != s.cid {
			http.Error(w, "invalid cid", http.StatusBadRequest)
			return
		}
		cmdCount++
	}
	if cmdCount != 1 {
		http.Error(w, "invalid number of commands", http.StatusBadRequest)
		return
	}
	if err := s.addSession(w).serve(r.Context(), r.Body); err != nil {
		s.logger.Error("session error", "err", err)
	}
}

func (s *Server) addSession(w http.ResponseWriter) *session {
	s.lock.Lock()
	defer s.lock.Unlock()
	// we're just using an increasing number, though it can be a random, unique string
	s.sessionID++
	sessionID := strconv.Itoa(s.sessionID)
	sess := session{
		w:         lineWriter{ResponseWriter: w},
		sessionID: sessionID,
		created:   time.Now(),
		server:    s,
		update:    make(chan AdapterUpdate),
		logger:    s.logger.With("sessionID", sessionID),
	}
	s.sessions[sessionID] = &sess
	return &sess
}

func (s *Server) control(w http.ResponseWriter, r *http.Request) {
	for cmd, err := range readControlCommands(r.Body) {
		if err != nil {
			http.Error(w, "invalid control request: "+err.Error(), http.StatusBadRequest)
			return
		}
		switch cmd.CommandType {
		case addCommand:
			if err = s.subscribe(cmd); err == nil {
				_, _ = io.WriteString(w, "REQOK,"+cmd.RequestID+"\n")
			} else {
				_, _ = io.WriteString(w, "REQERR,"+cmd.RequestID+",1,"+err.Error()+"\n")
			}
		default:
			// this is already handled by err != nil
			http.Error(w, "unsupported operation: "+string(cmd.CommandType), http.StatusBadRequest)
			return
		}
	}
}

func (s *Server) subscribe(cmd controlCommand) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	sess, ok := s.sessions[cmd.SessionID]
	if !ok {
		return errors.New("session not found")
	}
	adapterSet, ok := s.AdapterSets[cmd.DataAdapter]
	if !ok {
		return errors.New("data adapter not found")
	}
	group, ok := adapterSet[cmd.Group]
	if !ok {
		return errors.New("group not found")
	}
	return sess.subscribe(group, cmd.SubId, cmd.Mode, cmd.Schema)
}

////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

type session struct {
	created   time.Time
	lastWrite time.Time
	update    chan AdapterUpdate
	server    *Server
	logger    *slog.Logger
	sessionID string
	w         lineWriter
}

func (s *session) serve(ctx context.Context, r io.ReadCloser) error {
	defer func() { _ = r.Close() }()

	s.w.WriteHeader(http.StatusOK)

	s.w.Header().Set("Content-Type", "text/enriched; charset=UTF-8")
	s.w.Header().Add("Cache-Control", "no-store")
	s.w.Header().Add("Cache-Control", "no-transform")
	s.w.Header().Add("Cache-Control", "no-cache")
	s.w.Header().Add("Pragma", "no-cache")
	s.w.Header().Set("Transfer-Encoding", "chunked")

	_ = s.write("CONOK", s.sessionID, "5000", strconv.Itoa(keepAlivePeriodMilliSeconds), "*")
	_ = s.write("SERVNAME", "fake server")
	_ = s.write("CONS", "unlimited")

	syncTicker := time.NewTicker(20 * time.Second)
	defer syncTicker.Stop()

	probeTicker := time.NewTicker(time.Second)
	defer probeTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-syncTicker.C:
			s.sendSync()
		case <-probeTicker.C:
			if time.Since(s.w.LastWritten()) > keepAlivePeriodMilliSeconds*time.Millisecond {
				s.sendProbe()
			}
		case update := <-s.update:
			s.sendUpdate(update)
		}
	}
}

func (s *session) sendProbe() {
	_ = s.write("PROBE")
}

func (s *session) sendSync() {
	age := time.Since(s.created)
	_ = s.write("SYNC", strconv.Itoa(int(age.Seconds())))
}

func (s *session) sendUpdate(update AdapterUpdate) {
	_ = s.write("U", strconv.Itoa(update.SubscriptionID), strconv.Itoa(update.Item), update.Values.String())
}

func (s *session) write(elements ...string) error {
	line := strings.Join(elements, ",")
	s.logger.Debug("send", "line", line)
	s.w.WriteLine(line)
	return nil
}

func (s *session) subscribe(group Adapter, subId int, mode string, schema string) error {
	items, fields, err := group.Subscribe(s.update, subId, mode, schema)
	if err == nil {
		_ = s.write("SUBOK", strconv.Itoa(subId), strconv.Itoa(items), strconv.Itoa(fields))
	}
	s.logger.Debug("subscription requested", "subID", subId, "group", group.String(), "err", err)
	return err
}

type lineWriter struct {
	http.ResponseWriter
	lastWritten time.Time
	lock        sync.RWMutex
}

func (w *lineWriter) WriteLine(s string) {
	w.lock.Lock()
	defer w.lock.Unlock()
	_, _ = io.WriteString(w.ResponseWriter, s+"\r\n")
	w.ResponseWriter.(http.Flusher).Flush()
	w.lastWritten = time.Now()
}

func (w *lineWriter) LastWritten() time.Time {
	w.lock.RLock()
	defer w.lock.RUnlock()
	return w.lastWritten
}

type sessionCommand struct {
	AdapterSet string
	CID        string
}

func readSessionCommands(r io.ReadCloser) iter.Seq2[sessionCommand, error] {
	return func(yield func(sessionCommand, error) bool) {
		for values, err := range readCommands(r) {
			var cmd sessionCommand
			cmd, err = parseSessionCommand(values)
			if !yield(cmd, err) {
				return
			}
			if err != nil {
				return
			}
		}
	}
}

func parseSessionCommand(values url.Values) (cmd sessionCommand, err error) {
	if cmd.AdapterSet = values.Get("LS_adapter_set"); cmd.AdapterSet == "" {
		return cmd, errors.New("missing requested LS_adapter_set")
	}
	if cmd.CID = values.Get("LS_cid"); cmd.CID == "" {
		return cmd, errors.New("missing requested LS_cid")
	}
	return cmd, nil
}

type controlCommand struct {
	CommandType commandType
	SessionID   string
	RequestID   string
	DataAdapter string
	Group       string
	Mode        string
	Schema      string
	SubId       int
}

type commandType string

const (
	addCommand commandType = "add"
)

func readControlCommands(r io.ReadCloser) iter.Seq2[controlCommand, error] {
	return func(yield func(controlCommand, error) bool) {
		for values, err := range readCommands(r) {
			var cmd controlCommand
			cmd, err = parseControlCommand(values)
			if !yield(cmd, err) {
				return
			}
			if err != nil {
				return
			}
		}
	}
}

func parseControlCommand(values url.Values) (cmd controlCommand, err error) {
	cmd.CommandType = commandType(values.Get("LS_op"))
	if cmd.RequestID = values.Get("LS_reqId"); cmd.RequestID == "" {
		return cmd, errors.New("missing LS_reqId")
	}
	if cmd.SessionID = values.Get("LS_session"); cmd.SessionID == "" {
		return cmd, errors.New("missing LS_session")
	}
	switch cmd.CommandType {
	case addCommand:
		cmd.DataAdapter = values.Get("LS_data_adapter")
		cmd.Group = values.Get("LS_group")
		subId := values.Get("LS_subId")
		if cmd.SubId, err = strconv.Atoi(subId); err != nil {
			return cmd, fmt.Errorf("invalid LS_subId: %w", err)
		}
		cmd.Schema = values.Get("LS_schema")
		cmd.Mode = values.Get("LS_mode")
	default:
		return cmd, fmt.Errorf("missing/unsupported command type: %q", cmd.CommandType)
	}
	return cmd, nil
}

func readCommands(r io.ReadCloser) iter.Seq2[url.Values, error] {
	return func(yield func(url.Values, error) bool) {
		defer func() { _ = r.Close() }()
		lines := bufio.NewScanner(r)
		for lines.Scan() {
			if !yield(url.ParseQuery(lines.Text())) {
				return
			}
		}
	}
}
