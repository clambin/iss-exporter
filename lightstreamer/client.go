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
	"time"
)

const (
	serverURL     = "https://push.lightstreamer.com/lightstreamer"
	lsProto       = "TLCP-2.1.0"
	timeDiffLimit = 5
)

type Client struct {
	logger     *slog.Logger
	HTTPClient *http.Client
	ServerURL  string
	loginArgs  url.Values
}

type ClientOption func(*Client)

func NewClient(opt ...ClientOption) *Client {
	loginArgs := make(url.Values)
	loginArgs.Set("LS_cid", "mgQkwtwdysogQz2BJ4Ji%20kOj2Bg")

	c := Client{
		HTTPClient: http.DefaultClient,
		ServerURL:  serverURL,
		logger:     slog.Default(),
		loginArgs:  loginArgs,
	}
	for _, o := range opt {
		o(&c)
	}

	return &c
}

func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *Client) {
		c.logger = logger
	}
}

func WithServerURL(url string) ClientOption {
	return func(c *Client) {
		c.ServerURL = url
	}
}

func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		c.HTTPClient = client
	}
}

func WithAdapterSet(adapterSet string) ClientOption {
	return func(c *Client) {
		c.loginArgs.Set("LS_adapter_set", adapterSet)
	}
}

func WithCID(cid string) ClientOption {
	return func(c *Client) {
		c.loginArgs.Set("LS_cid", cid)
	}
}

func WithCredentials(username, password string) ClientOption {
	return func(c *Client) {
		c.loginArgs.Set("LS_user", username)
		c.loginArgs.Set("LS_password", password)
	}
}

func WithContentLength(length uint) ClientOption {
	return func(c *Client) {
		c.loginArgs.Set("LS_content_length", strconv.FormatUint(uint64(length), 10))
	}
}

func (s *Client) Connect(ctx context.Context) (*Session, error) {
	clientSession := Session{
		logger:        s.logger,
		subscriptions: make(map[int]*subscription),
		serverURL:     cmp.Or(s.ServerURL, serverURL),
		httpClient:    cmp.Or(s.HTTPClient, http.DefaultClient),
	}

	err := clientSession.run(ctx, s.loginArgs)
	// TODO: wait for connection to be established here?
	return &clientSession, err
}

////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

type subscription struct {
	last   map[int]Values
	update UpdateFunc
}

type UpdateFunc func(item int, values Values)

func (s *subscription) Update(item int, values Values) error {
	if s.last == nil {
		s.last = make(map[int]Values)
	}
	next, err := s.last[item].Update(values)
	if err == nil {
		s.last[item] = next
		s.update(item, next)
	}
	return err
}

////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

type Session struct {
	logger         *slog.Logger
	httpClient     *http.Client
	subscriptions  map[int]*subscription
	sessionID      string
	serverURL      string
	requestLimit   int
	keepAliveTime  int
	requestID      int
	subscriptionID int
	createdTime    time.Time
	lock           sync.RWMutex
}

func (s *Session) Connected() bool {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.sessionID != ""
}

func (s *Session) WaitOnConnection(ctx context.Context, duration time.Duration) error {
	start := time.Now()
	for !s.Connected() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
			if time.Since(start) > duration {
				return errors.New("timeout waiting for connection")
			}
		}
	}
	return nil
}

func (s *Session) run(ctx context.Context, loginArgs url.Values) error {
	r, err := s.connect(ctx, loginArgs)
	if err == nil {
		go s.handleSession(ctx, r)
	}
	return err
}

func (s *Session) connect(ctx context.Context, loginArgs url.Values) (io.ReadCloser, error) {
	resp, err := s.call(ctx, "create_session", loginArgs)
	if err != nil {
		return nil, err
	}
	s.createdTime = time.Now()
	return resp.Body, nil
}

func (s *Session) handleSession(ctx context.Context, r io.ReadCloser) {
	for r != nil {
		select {
		case <-ctx.Done():
			return
		default:
		}

		rebind, err := s.handleConnection(r)
		if err != nil {
			s.logger.Error("error while handling connection", "err", err)
			return
		}
		r = nil
		if rebind {
			s.logger.Debug("rebinding connection")
			if r, err = s.rebind(ctx); err != nil {
				s.logger.Error("error while rebinding connection", "err", err)
			}
		}
	}
}

func (s *Session) rebind(ctx context.Context) (io.ReadCloser, error) {
	parameters := make(url.Values)
	parameters.Set("LS_session", s.sessionID)

	resp, err := s.call(ctx, "bind_session", parameters)
	if err != nil {
		return nil, err
	}
	s.createdTime = time.Now()
	return resp.Body, nil
}

func (s *Session) handleConnection(r io.ReadCloser) (bool, error) {
	defer func() { _ = r.Close() }()
	for msg, err := range client.Messages(r) {
		if err != nil {
			return false, fmt.Errorf("parse: %w", err)
		}
		s.logger.Debug("< "+string(msg.MessageType), "data", msg.Data)
		switch data := msg.Data.(type) {
		case client.CONOKData:
			err = s.handleConnectionOK(data)
		case client.UData:
			err = s.handleUpdate(data)
		case client.SYNCData:
			err = s.handleSync(data)
		case client.LOOPData:
			s.logger.Debug("session should be rebound", "delay", data.ExpectedDelay)
			return true, nil
		case client.ENDData:
			s.logger.Info("session terminated", "code", data.Code, "msg", data.Message)
		}
		if err != nil {
			s.logger.Error("error handling message", "msgType", msg.MessageType, "err", err)
		}
	}
	return false, nil
}

func (s *Session) handleConnectionOK(data client.CONOKData) (err error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.sessionID = data.SessionID
	s.requestLimit = data.RequestLimit
	s.keepAliveTime = data.KeepAliveTime
	s.logger.Debug("Connected", "sessionID", s.sessionID)
	return nil
}

func (s *Session) handleUpdate(data client.UData) error {
	s.lock.RLock()
	defer s.lock.RUnlock()
	sub, ok := s.subscriptions[data.SubscriptionID]
	if !ok {
		return fmt.Errorf("unknown subscription ID %d", data.SubscriptionID)
	}
	return sub.Update(data.Item, data.Values)
}

func (s *Session) handleSync(data client.SYNCData) error {
	s.lock.RLock()
	defer s.lock.RUnlock()
	serverAge := data.SecondsSinceInitialHeader
	clientAge := int(time.Since(s.createdTime).Seconds())
	s.logger.Debug("time sync check", "serverAge", serverAge, "clientAge", clientAge)
	if diff := clientAge - serverAge; math.Abs(float64(diff)) > timeDiffLimit {
		s.logger.Warn("client/server time difference", "diff", diff)
	}
	return nil
}

func (s *Session) Subscribe(ctx context.Context, adapter string, group string, schema []string, f UpdateFunc) error {
	if !s.Connected() {
		return errors.New("client is not connected")
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	s.requestID++
	s.subscriptionID++
	parameters := make(url.Values)
	parameters.Set("LS_op", "add")
	parameters.Set("LS_reqId", strconv.Itoa(s.requestID))
	parameters.Set("LS_session", s.sessionID)
	parameters.Set("LS_subId", strconv.Itoa(s.subscriptionID))
	parameters.Set("LS_data_adapter", adapter)
	parameters.Set("LS_group", group)
	parameters.Set("LS_schema", strings.Join(schema, " "))
	parameters.Set("LS_mode", "MERGE")
	//parameters.Set("LS_requested_max_frequency", "0.1") // TODO: make this is a parameter

	resp, err := s.call(ctx, "control", parameters)
	if err != nil {
		return err
	}

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	body = bytes.TrimSuffix(body, []byte("\r\n"))
	parts := strings.Split(string(body), ",")

	if len(parts) == 0 {
		return errors.New("unexpected empty response")
	}

	switch parts[0] {
	case "REQOK":
		s.subscriptions[s.subscriptionID] = &subscription{update: f}
		return nil
	case "REQERR":
		if len(parts) != 4 {
			return fmt.Errorf("expected 3 arguments, got %d", len(parts))
		}
		return fmt.Errorf("%s: %s", parts[2], parts[3])
	default:
		return fmt.Errorf("subscription failed: unexpected response %q", parts[0])
	}
}

func (s *Session) call(ctx context.Context, endpoint string, values url.Values) (*http.Response, error) {
	req, err := s.makeRequest(ctx, endpoint, values)
	if err != nil {
		return nil, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if body = bytes.TrimSuffix(body, []byte("\r\n")); len(body) > 0 {
			return nil, fmt.Errorf("%s (%s)", resp.Status, string(body))
		}
		return nil, fmt.Errorf("http: %s", resp.Status)
	}
	return resp, nil
}

func (s *Session) makeRequest(ctx context.Context, endpoint string, values url.Values) (*http.Request, error) {
	args := make(url.Values)
	args.Set("LS_protocol", lsProto)

	reqURL := s.serverURL + "/" + endpoint + ".txt?" + args.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req, nil
}
