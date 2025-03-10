package lightstreamer

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"github.com/clambin/iss-exporter/lightstreamer/internal/client"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	serverURL  = "https://push.lightstreamer.com/lightstreamer"
	defaultCID = "mgQkwtwdysogQz2BJ4Ji%20kOj2Bg"
	lsProtocol = "TLCP-2.1.0"
)

// A ClientSession establishes and manages a client session with a LightStreamer server.
// Its main usage is to subscribe to one or more feeds from the server and receive updates for those subscriptions.
type ClientSession struct {
	sessionID           atomic.Value
	sessionCreationTime atomic.Value
	httpClient          *http.Client
	parameters          url.Values
	cancelFunc          context.CancelFunc
	logger              *slog.Logger
	serverURL           string
	subscriptions       subscriptions
	subscriptionID      atomic.Int32
	requestID           atomic.Int32
	connections         atomic.Int32
	timeDifference      atomic.Int32
}

// NewClientSession returns a new client session with a LightStreamer server.
// Use ClientSessionOption arguments to configure the session.
func NewClientSession(options ...ClientSessionOption) *ClientSession {
	c := ClientSession{
		serverURL:  serverURL,
		httpClient: http.DefaultClient,
		parameters: url.Values{"LS_cid": []string{defaultCID}},
		logger:     slog.New(slog.DiscardHandler),
	}
	for _, o := range options {
		o(&c)
	}
	return &c
}

// Connect establishes a connection with the LightStreamer server and processes all incoming updates.
//
// Note: on return, the session is still in an unbound state and calling Subscribe will fail.
// Use SessionEstablished to wait for the session to be bound.
func (c *ClientSession) Connect(ctx context.Context) error {
	ctx, c.cancelFunc = context.WithCancel(ctx)
	r, err := c.createSession(ctx)
	if err == nil {
		go func() { _ = c.serve(ctx, r) }()
	}
	return err
}

// Disconnect closes the connection to the LightStreamer server.
func (c *ClientSession) Disconnect() {
	if c.cancelFunc != nil {
		c.cancelFunc()
	}
}

// SessionEstablished waits for the session to be bound, or the context to be canceled.
func (c *ClientSession) SessionEstablished(ctx context.Context) error {
	for {
		if sessionID, ok := c.sessionID.Load().(string); ok && sessionID != "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// ConnectWithSession is a convenience function that opens a connection and waits for a session to be established.
func (c *ClientSession) ConnectWithSession(ctx context.Context, timeout time.Duration) error {
	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := c.SessionEstablished(ctx); err != nil {
		return fmt.Errorf("session: %w", err)
	}
	return nil
}

func (c *ClientSession) serve(ctx context.Context, r io.ReadCloser) error {
	c.logger.Debug("serving connection", "count", c.connections.Add(1))
	defer func() {
		c.logger.Debug("connection closed", "count", c.connections.Add(-1))
		_ = r.Close()
	}()
	ch := make(chan client.Message)
	done := make(chan struct{})
	// read messages in a separate go routine we can terminate when ctx is canceled.
	// go routine stops when we close r
	go readAllMessages(r, ch, done)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return nil
		case msg := <-ch:
			c.handleMessage(ctx, msg)
		}
	}
}

func readAllMessages(r io.Reader, ch chan client.Message, done chan struct{}) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if msg, err := client.ParseSessionMessage(scanner.Text()); err == nil {
			ch <- msg
		}
	}
	done <- struct{}{}
}

func (c *ClientSession) handleMessage(ctx context.Context, msg client.Message) {
	switch data := msg.Data.(type) {
	case client.CONOKData:
		c.sessionID.Store(data.SessionID)
		c.logger.Debug("session established", "sessionID", data.SessionID)
	case client.PROGData, client.NOOPData, client.SERVNAMEData, client.CLIENTIPData, client.CONSData,
		client.CONFData, client.SUBOKData, client.PROBEData:
	case client.UData:
		c.handleUpdate(data)
	case client.SYNCData:
		c.handleSync(data)
	case client.LOOPData:
		go c.handleLoop(ctx, data)
	case client.ENDData:
		c.logger.Debug("connection closing", "data", data)
	default:
		c.logger.Debug("received message", "msg", msg)
	}
}

func (c *ClientSession) handleLoop(ctx context.Context, data client.LOOPData) {
	c.logger.Debug("rebinding session", "delay", data.ExpectedDelay)
	if data.ExpectedDelay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(data.ExpectedDelay) * time.Second):
		}
	}
	if r, err := c.rebind(ctx, c.sessionID.Load().(string)); err == nil {
		go func() { _ = c.serve(ctx, r) }()
	}
}

func (c *ClientSession) handleSync(data client.SYNCData) {
	var delta int
	if cTime, ok := c.sessionCreationTime.Load().(time.Time); ok {
		sessionOpenTime := int(time.Since(cTime).Seconds())
		delta = data.SecondsSinceInitialHeader - sessionOpenTime
	}
	c.timeDifference.Store(int32(delta))
	c.logger.Debug("time sync", "delta", time.Duration(delta)*time.Second)
}

func (c *ClientSession) handleUpdate(data client.UData) {
	sub, ok := c.subscriptions.get(data.SubscriptionID)
	if !ok {
		c.logger.Warn("no subscription found for update", "subscriptionID", data.SubscriptionID)
	}
	_ = sub.update(data.Item, data.Values)
}

// Subscribe registers a new subscription with the server for the specified adapter & group, asking for data adhering to the specified schema.
// Any received updates are passed to the provided UpdateFunc.
//
// If maxFrequency is non-zero, Subscribe asks for data to be sent at the specified maximum frequency (in updates per second).
//
// Notes:
//   - all subscriptions are in "MERGE" mode.
//   - adapter, group & schema are application-specific and not validated by ClientSession.
//   - maxFrequency may be ignored by the server. ClientSession does not provide any throttling.
func (c *ClientSession) Subscribe(ctx context.Context, adapter string, group string, schema []string, maxFrequency float64, f func(item int, values Values)) error {
	if c.sessionID.Load() == nil {
		return errors.New("no session")
	}

	subID, r, err := c.addSubscription(ctx, adapter, group, schema, maxFrequency)
	if err != nil {
		return err
	}

	body, _ := io.ReadAll(r)
	_ = r.Close()
	body = bytes.TrimSuffix(body, []byte("\n"))
	body = bytes.TrimSuffix(body, []byte("\r"))

	msg, err := client.ParseControlMessage(string(body))
	if err != nil {
		return fmt.Errorf("unexpected response: %w", err)
	}
	switch data := msg.Data.(type) {
	case client.REQOKData:
		c.subscriptions.add(subID, &subscription{onUpdate: f})
		return nil
	case client.REQERRData:
		return fmt.Errorf("%d: %s", data.ErrorCode, data.ErrorMessage)
	default:
		return fmt.Errorf("subscription failed: unexpected response type %q", msg.MessageType)
	}
}

func (c *ClientSession) createSession(ctx context.Context) (io.ReadCloser, error) {
	r, err := c.call(ctx, "create_session", c.parameters)
	if err == nil {
		c.sessionCreationTime.Store(time.Now())
	}
	return r, err
}

func (c *ClientSession) rebind(ctx context.Context, sessionID string) (io.ReadCloser, error) {
	parameters := make(url.Values)
	parameters.Set("LS_session", sessionID)
	r, err := c.call(ctx, "bind_session", parameters)
	if err == nil {
		c.sessionCreationTime.Store(time.Now())
	}
	return r, err
}

func (c *ClientSession) addSubscription(ctx context.Context, adapter string, group string, schema []string, maxFrequency float64) (int, io.ReadCloser, error) {
	subID := int(c.subscriptionID.Add(1))
	parameters := make(url.Values)
	parameters.Set("LS_op", "add")
	parameters.Set("LS_reqId", strconv.Itoa(int(c.requestID.Add(1))))
	parameters.Set("LS_session", c.sessionID.Load().(string))
	parameters.Set("LS_subId", strconv.Itoa(subID))
	parameters.Set("LS_data_adapter", adapter)
	parameters.Set("LS_group", group)
	parameters.Set("LS_schema", strings.Join(schema, " "))
	parameters.Set("LS_mode", "MERGE")
	if maxFrequency > 0 {
		parameters.Set("LS_requested_max_frequency", strconv.FormatFloat(maxFrequency, 'f', -1, 64))
	}

	r, err := c.call(ctx, "control", parameters)
	return subID, r, err
}

var encodedArgs = url.Values{"LS_protocol": []string{lsProtocol}}.Encode()

func (c *ClientSession) call(ctx context.Context, endpoint string, values url.Values) (io.ReadCloser, error) {
	reqURL := c.serverURL + "/" + endpoint + ".txt?" + encodedArgs
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		return nil, lsError(resp)
	}
	return resp.Body, nil
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
		c.parameters.Set("LS_adapter_set", adapterSet)
	}
}

// WithCID sets the CID to use to create the session. The default is "mgQkwtwdysogQz2BJ4Ji%20kOj2Bg".
func WithCID(cid string) ClientSessionOption {
	return func(c *ClientSession) {
		c.parameters.Set("LS_cid", cid)
	}
}

/*
	func WithCredentials(username, password string) ClientSessionOption {
		return func(c *ClientSession) {
			c.loginArgs.Set("LS_user", username)
			c.loginArgs.Set("LS_password", password)
		}
	}
*/

func WithContentLength(length uint) ClientSessionOption {
	return func(c *ClientSession) {
		c.parameters.Set("LS_content_length", strconv.FormatUint(uint64(length), 10))
	}
}
