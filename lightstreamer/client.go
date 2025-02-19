package lightstreamer

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	serverURL = "https://push.lightstreamer.com/lightstreamer"
	lsProto   = "TLCP-2.1.0"
)

type Client struct {
	logger     *slog.Logger
	HTTPClient *http.Client
	Set        string
	CID        string
	ServerURL  string
}

func NewClient(set string, cid string, logger *slog.Logger) *Client {
	return &Client{
		Set:        set,
		CID:        cid,
		HTTPClient: http.DefaultClient,
		ServerURL:  serverURL,
		logger:     logger,
	}
}

func (s *Client) Connect(ctx context.Context) (*ClientConnection, error) {
	clientConnection := ClientConnection{
		logger:        s.logger,
		subscriptions: make(map[int]*subscription),
		serverURL:     cmp.Or(s.ServerURL, serverURL),
		httpClient:    cmp.Or(s.HTTPClient, http.DefaultClient),
	}

	r, err := clientConnection.connect(ctx, s.Set, s.CID)
	if err != nil {
		return nil, err
	}
	go clientConnection.handleConnection(r)
	return &clientConnection, nil
}

type subscription struct {
	update UpdateFunc
	logger *slog.Logger
}

type UpdateFunc func(item int, values []string, l *slog.Logger)

type ClientConnection struct {
	logger         *slog.Logger
	lock           sync.RWMutex
	sessionID      string
	requestLimit   int
	keepAliveTime  int
	subscriptions  map[int]*subscription
	requestID      int
	subscriptionID int
	serverURL      string
	httpClient     *http.Client
}

func (c *ClientConnection) Connected() bool {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.sessionID != ""
}

func (c *ClientConnection) WaitOnConnection(ctx context.Context, duration time.Duration) error {
	start := time.Now()
	for !c.Connected() {
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

func (c *ClientConnection) connect(ctx context.Context, set string, cid string) (io.ReadCloser, error) {
	parameters := make(url.Values)
	parameters.Set("LS_adapter_set", set)
	parameters.Set("LS_cid", cid)

	req, err := c.makeRequest(ctx, "create_session", parameters)
	if err != nil {
		return nil, err
	}

	c.logger.Info("Connecting to LightStreamer", "url", req.URL)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		body = bytes.TrimSuffix(body, []byte("\n"))
		body = bytes.TrimSuffix(body, []byte("\r"))
		if len(body) > 0 {
			return nil, fmt.Errorf("%s (%s)", resp.Status, string(body))
		}
		return nil, fmt.Errorf("http: %s", resp.Status)
	}

	return resp.Body, nil
}

func (c *ClientConnection) handleConnection(r io.ReadCloser) {
	defer func() { _ = r.Close() }()
	lines := bufio.NewScanner(r)
	for lines.Scan() {
		parts := strings.Split(lines.Text(), ",")
		if len(parts) == 0 {
			continue
		}
		var err error
		switch parts[0] {
		case "CONOK":
			//c.logger.Debug("CONOK", "args", parts[1:])
			err = c.handleConnectionOK(parts[1:])
		case "U":
			//c.logger.Debug("U", "parts", parts[1:])
			err = c.handleUpdate(parts[1:])
		case "NOOP", "PROBE", "SYNC", "SERVNAME", "CLIENTIP", "CONS", "SUBOK", "CONF", "PROG":
			//c.logger.Debug(parts[0], "params", parts[1:])
		default:
			c.logger.Debug(parts[0], "params", parts[1:])
		}

		if err != nil {
			c.logger.Warn("error processing "+parts[0], "err", err)
		}
	}
}

func (c *ClientConnection) handleConnectionOK(parts []string) (err error) {
	// <session-ID>,<request-limit>,<keep-alive>,<control-link>
	if len(parts) != 4 {
		return fmt.Errorf("expected 4 arguments, got %d", len(parts))
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	c.sessionID = parts[0]
	if c.requestLimit, err = strconv.Atoi(parts[1]); err != nil {
		return fmt.Errorf("invalid request limit %q: %w", parts[1], err)
	}
	if c.keepAliveTime, err = strconv.Atoi(parts[2]); err != nil {
		return fmt.Errorf("invalid keep alive time %q: %w", parts[2], err)
	}
	c.logger.Debug("Connected", "sessionID", c.sessionID)
	return nil
}

func (c *ClientConnection) handleUpdate(parts []string) error {
	//U,<subscription-ID>,<item>,<field-1-value>|<field-2-value>|...|<field-N-value>
	if len(parts) < 3 {
		return fmt.Errorf("expected 3+ arguments, got %d", len(parts))
	}
	subID, err := strconv.Atoi(parts[0])
	if err != nil {
		return fmt.Errorf("invalid subscription ID %q: %w", parts[0], err)
	}
	item, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("invalid item %q: %w", parts[1], err)
	}
	c.lock.RLock()
	defer c.lock.RUnlock()
	sub, ok := c.subscriptions[subID]
	if !ok {
		return fmt.Errorf("unknown subscription ID %d", subID)
	}
	sub.update(item, parts[2:], sub.logger)
	return nil
}

func (c *ClientConnection) Subscribe(ctx context.Context, adapter string, group string, schema []string, f UpdateFunc) error {
	if !c.Connected() {
		return errors.New("client is not connected")
	}

	c.lock.Lock()
	defer c.lock.Unlock()

	c.requestID++
	c.subscriptionID++

	parameters := make(url.Values)
	parameters.Set("LS_op", "add")
	parameters.Set("LS_reqId", strconv.Itoa(c.requestID))
	parameters.Set("LS_session", c.sessionID)
	parameters.Set("LS_subId", strconv.Itoa(c.subscriptionID))
	parameters.Set("LS_data_adapter", adapter)
	parameters.Set("LS_group", group)
	parameters.Set("LS_schema", strings.Join(schema, " "))
	parameters.Set("LS_mode", "MERGE")
	//parameters.Set("LS_requested_max_frequency", "0.1") // TODO: make this is a parameter

	req, err := c.makeRequest(ctx, "control", parameters)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
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
		l := c.logger.With(
			slog.Group("subscription",
				slog.String("adapter", adapter),
				slog.String("group", group),
			),
		)
		c.subscriptions[c.subscriptionID] = &subscription{logger: l, update: f}
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

func (c *ClientConnection) makeRequest(ctx context.Context, endpoint string, values url.Values) (*http.Request, error) {
	args := make(url.Values)
	args.Set("LS_protocol", lsProto)

	reqURL := c.serverURL + "/" + endpoint + ".txt?" + args.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req, nil
}
