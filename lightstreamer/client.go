package lightstreamer

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"github.com/clambin/iss-exporter/lightstreamer/internal/client"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	serverURL     = "https://push.lightstreamer.com/lightstreamer"
	defaultCID    = "mgQkwtwdysogQz2BJ4Ji%20kOj2Bg"
	lsProtocol    = "TLCP-2.1.0"
	timeDiffLimit = 5
)

////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

// A ClientSession manages a client session with a LightStreamer server.  Its main usage is to manage subscriptions to one or more feeds from the server's Data Adapter Set.
type ClientSession struct {
	createdTime     atomic.Value
	sessionID       atomic.Value
	logger          *slog.Logger
	httpClient      *http.Client
	loginArgs       url.Values
	serverURL       string
	subscriptions   subscriptions
	connectTimeout  time.Duration
	openConnections atomic.Int32
	requestID       atomic.Int32
	subscriptionID  atomic.Int32
}

// NewClientSession returns a new client session with a LightStreamer server.
// Use ClientSessionOption arguments to configure the session.
//
// Callers can rely on the session being bound (and ready for subscription requests) when NewClientSession returns.
func NewClientSession(ctx context.Context, opts ...ClientSessionOption) (*ClientSession, error) {
	clientSession := ClientSession{
		logger: slog.New(slog.DiscardHandler),
		//subscriptions:  make(map[int]*subscription),
		serverURL:      serverURL,
		httpClient:     http.DefaultClient,
		connectTimeout: 5 * time.Second,
		loginArgs:      url.Values{"LS_cid": []string{defaultCID}},
	}
	for _, o := range opts {
		o(&clientSession)
	}
	err := clientSession.createSession(ctx, clientSession.loginArgs)
	if err == nil {
		err = clientSession.waitOnConnection(ctx, clientSession.connectTimeout)
	}
	return &clientSession, err

}

// Bound returns true if the client session is currently bound to the server, i.e. the client can subscribe to a feed.
func (s *ClientSession) Bound() bool {
	return s.sessionID.Load() != nil
}

func (s *ClientSession) createSession(ctx context.Context, loginArgs url.Values) error {
	r, err := s.connect(ctx, loginArgs)
	if err == nil {
		go s.handleSession(ctx, r)
	}
	return err
}

func (s *ClientSession) handleSession(ctx context.Context, r io.ReadCloser) {
	defer func() {
		s.logger.Debug("connection closed", "count", s.openConnections.Add(-1))
		_ = r.Close()
	}()
	s.logger.Debug("connection opened", "count", s.openConnections.Add(1))
	ch := client.SessionMessages(r, s.logger)
	for {
		select {
		case <-ctx.Done():
			s.logger.Debug("context done", "err", ctx.Err())
			return
		case msg, ok := <-ch:
			if !ok {
				s.logger.Debug("connection closed")
				return
			}
			var err error
			switch data := msg.Data.(type) {
			case client.CONOKData:
				err = s.handleConnectionOK(data)
			case client.UData:
				err = s.handleUpdate(data)
			case client.SYNCData:
				err = s.handleSync(data)
			case client.LOOPData:
				err = s.handleLoop(ctx, data)
			case client.ENDData:
				s.logger.Info("end", "code", data.Code, "msg", data.Message)
			}
			if err != nil {
				s.logger.Error("error handling message", "msgType", msg.MessageType, "err", err)
			}
		}
	}
}

func (s *ClientSession) waitOnConnection(ctx context.Context, duration time.Duration) error {
	subCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	for {
		select {
		case <-subCtx.Done():
			return subCtx.Err()
		case <-time.After(100 * time.Millisecond):
			if s.Bound() {
				return nil
			}
		}
	}
}

func (s *ClientSession) connect(ctx context.Context, parameters url.Values) (io.ReadCloser, error) {
	resp, err := s.call(ctx, "create_session", parameters)
	if err != nil {
		return nil, err
	}
	s.createdTime.Store(time.Now())
	return resp.Body, nil
}

func (s *ClientSession) rebind(ctx context.Context) (io.ReadCloser, error) {
	sessionID := s.sessionID.Load()
	if sessionID == nil {
		return nil, errors.New("can't rebind unbound session")
	}
	parameters := make(url.Values)
	parameters.Set("LS_session", sessionID.(string))

	resp, err := s.call(ctx, "bind_session", parameters)
	if err != nil {
		return nil, err
	}
	s.createdTime.Store(time.Now())
	return resp.Body, nil
}

func (s *ClientSession) handleConnectionOK(data client.CONOKData) (err error) {
	s.sessionID.Store(data.SessionID)
	s.logger.Debug("session is bound", "sessionID", s.sessionID)
	return nil
}

// TODO: this could be used to determine time difference between client & server and then include the server time in Message
func (s *ClientSession) handleSync(data client.SYNCData) error {
	serverAge := data.SecondsSinceInitialHeader
	created := s.createdTime.Load()
	if created == nil {
		// this should never happen
		return fmt.Errorf("received sync on unbound session")

	}
	clientAge := int(time.Since(created.(time.Time)).Seconds())
	s.logger.Debug("time sync check", "serverAge", serverAge, "clientAge", clientAge)
	if diff := clientAge - serverAge; math.Abs(float64(diff)) > timeDiffLimit {
		s.logger.Warn("client/server time difference", "diff", diff)
	}
	return nil
}

func (s *ClientSession) handleLoop(ctx context.Context, data client.LOOPData) error {
	s.logger.Debug("loop", "expectedDelay", data.ExpectedDelay)
	if data.ExpectedDelay > 0 {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(data.ExpectedDelay) * time.Second):
		}
	}
	r, err := s.rebind(ctx)
	if err == nil {
		go s.handleSession(ctx, r)
	}
	return err
}

func (s *ClientSession) handleUpdate(data client.UData) error {
	if sub, ok := s.subscriptions.get(data.SubscriptionID); ok {
		return sub.update(data.Item, data.Values)
	}
	return fmt.Errorf("unknown subscription ID %d", data.SubscriptionID)
}

// Subscribe registers a new subscription with the server for the specified adapter & group, asking for data adhering to the specified schema.
// Any received updates are passed to the provided UpdateFunc.
//
// If maxFrequency is non-zero, Subscribe asks for data to be sent at the specified maximum frequency (in updates per second).
//
// Notes:
//   - adapter, group & schema are application-specific and not validated by ClientSession.
//   - maxFrequency may be ignored by the server. ClientSession does not provide any throttling.
func (s *ClientSession) Subscribe(ctx context.Context, adapter string, group string, schema []string, maxFrequency float64, f UpdateFunc) error {
	if !s.Bound() {
		return errors.New("client is not connected")
	}

	subID := int(s.subscriptionID.Add(1))
	parameters := make(url.Values)
	parameters.Set("LS_op", "add")
	parameters.Set("LS_reqId", strconv.Itoa(int(s.requestID.Add(1))))
	parameters.Set("LS_session", s.sessionID.Load().(string))
	parameters.Set("LS_subId", strconv.Itoa(subID))
	parameters.Set("LS_data_adapter", adapter)
	parameters.Set("LS_group", group)
	parameters.Set("LS_schema", strings.Join(schema, " "))
	parameters.Set("LS_mode", "MERGE")
	if maxFrequency > 0 {
		parameters.Set("LS_requested_max_frequency", strconv.FormatFloat(maxFrequency, 'f', -1, 64))
	}

	resp, err := s.call(ctx, "control", parameters)
	if err != nil {
		return err
	}

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	body = bytes.TrimSuffix(body, []byte("\n"))
	body = bytes.TrimSuffix(body, []byte("\r"))

	msg, err := client.ParseControlMessage(string(body))
	if err != nil {
		return fmt.Errorf("unexpected response: %w", err)
	}
	switch data := msg.Data.(type) {
	case client.REQOKData:
		s.subscriptions.add(subID, &subscription{onUpdate: f})
		return nil
	case client.REQERRData:
		return fmt.Errorf("%d: %s", data.ErrorCode, data.ErrorMessage)
	default:
		return fmt.Errorf("subscription failed: unexpected response %q", msg.MessageType)
	}
}

var encodedArgs = url.Values{"LS_protocol": []string{lsProtocol}}.Encode()

func (s *ClientSession) call(ctx context.Context, endpoint string, values url.Values) (*http.Response, error) {
	reqURL := s.serverURL + "/" + endpoint + ".txt?" + encodedArgs
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		return nil, lsError(resp)
	}
	return resp, nil
}

func lsError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	body = bytes.TrimSuffix(body, []byte("\n"))
	body = bytes.TrimSuffix(body, []byte("\r"))
	if len(body) > 0 {
		return fmt.Errorf("lightstreamer: %s", string(body))
	}
	return fmt.Errorf("http: %d %s", resp.StatusCode, cmp.Or(resp.Status, http.StatusText(resp.StatusCode)))
}

////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

// ClientSessionOption configures a ClientSession.
type ClientSessionOption func(*ClientSession)

// WithLogger configures a slog.Logger for the ClientSession.  The default is slog.Default().
func WithLogger(logger *slog.Logger) ClientSessionOption {
	return func(c *ClientSession) {
		c.logger = logger
	}
}

// WithServerURL sets the Server URL. The default is https://push.lightstreamer.com/lightstreamer.
func WithServerURL(url string) ClientSessionOption {
	return func(c *ClientSession) {
		c.serverURL = url
	}
}

// WithHTTPClient sets the http.Client to interact with the server. The default is http.DefaultClient.
func WithHTTPClient(client *http.Client) ClientSessionOption {
	return func(c *ClientSession) {
		c.httpClient = client
	}
}

// WithAdapterSet sets the Adapter Set to use to create the session. There is no default.
func WithAdapterSet(adapterSet string) ClientSessionOption {
	return func(c *ClientSession) {
		c.loginArgs.Set("LS_adapter_set", adapterSet)
	}
}

// WithCID sets the CID to use to create the session. The default is "mgQkwtwdysogQz2BJ4Ji%20kOj2Bg".
func WithCID(cid string) ClientSessionOption {
	return func(c *ClientSession) {
		c.loginArgs.Set("LS_cid", cid)
	}
}

/*
	func WithCredentials(username, password string) ClientSessionOption {
		return func(c *ClientSession) {
			c.loginArgs.Set("LS_user", username)
			c.loginArgs.Set("LS_password", password)
		}
	}

func WithContentLength(length uint) ClientSessionOption {
	return func(c *ClientSession) {
		c.loginArgs.Set("LS_content_length", strconv.FormatUint(uint64(length), 10))
	}
}
*/

// WithBindTimeout specifies how long NewClientSession waits for the session to be bound. If the timeout is exceeded, NewClientSession returns an error.
func WithBindTimeout(timeout time.Duration) ClientSessionOption {
	return func(c *ClientSession) {
		c.connectTimeout = timeout
	}
}

////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

type subscription struct {
	last     map[int]Values
	onUpdate UpdateFunc
}

// UpdateFunc is called for every update received from the server, with update's item number and its Values.
// The Values are fully decoded & processed, so the callback always receives a complete update.
type UpdateFunc func(item int, values Values)

func (s *subscription) update(item int, values []string) error {
	if s.last == nil {
		s.last = make(map[int]Values)
	}
	next, err := s.last[item].Update(values)
	if err == nil {
		s.last[item] = next
		s.onUpdate(item, next)
	}
	return err
}

type subscriptions struct {
	items map[int]*subscription
	lock  sync.RWMutex
}

func (s *subscriptions) add(item int, sub *subscription) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.items == nil {
		s.items = make(map[int]*subscription)
	}
	s.items[item] = sub
}

func (s *subscriptions) get(item int) (*subscription, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	sub, ok := s.items[item]
	return sub, ok
}
