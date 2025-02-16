// Package lightstreamer provides a (very) basic implementation of the LightStreamer protocol.
// This is in no way production-ready.
//
// Ref: https://www.lightstreamer.com/sdks/ls-generic-client/2.1.0/TLCP%20Specifications.pdf
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
	"sync/atomic"
)

const ServerURL = "https://push.lightstreamer.com/lightstreamer"

type Client struct {
	Logger        *slog.Logger
	subscriptions map[string]*subscription
	Set           string
	CID           string
	connectionID  string
	serverURL     string
	requestLimit  int
	keepAliveTime int
	Connected     atomic.Bool
	reqId         atomic.Int32
	subId         atomic.Int32
}

func NewClient(set, cid string, logger *slog.Logger) *Client {
	return &Client{
		Set:           set,
		CID:           cid,
		Logger:        logger,
		subscriptions: make(map[string]*subscription),
	}
}

func (c *Client) Run(ctx context.Context) error {
	r, err := c.connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	lines := bufio.NewScanner(r)
	for lines.Scan() {
		parts := strings.Split(lines.Text(), ",")
		if len(parts) == 0 {
			continue
		}
		switch parts[0] {
		case "CONOK":
			c.connectionID = parts[1]
			c.requestLimit, _ = strconv.Atoi(parts[2])
			c.keepAliveTime, _ = strconv.Atoi(parts[3])
			c.Connected.Store(true)
			c.Logger.Debug("CONOK", "id", c.connectionID, "keepalive", c.keepAliveTime)
		case "SERVNAME":
			c.Logger.Debug("SERVNAME", "name", parts[1])
		case "CLIENTIP":
			c.Logger.Debug("CLIENTIP", "ip", parts[1])
		case "CONS":
			c.Logger.Debug("CONS", "bandwidth", parts[1])
		case "NOOP":
			// ignore
		case "PROBE":
			c.Logger.Debug("PROBE")
		case "SUBOK":
			c.Logger.Debug("SUBOK", "subID", parts[1], "items", parts[2], "fields", parts[3:])
		case "CONF":
			c.Logger.Debug("CONF", "subID", parts[1], "max-frequency", parts[2], "filtered", parts[3] == "filtered")
		case "SYNC":
			c.Logger.Debug("SYNC", "seconds", parts[1])
		case "U":
			//c.Logger.Debug("U", "args", parts[1:])
			if err = c.processUpdate(parts); err != nil {
				c.Logger.Warn("error processing update", "err", err)
			}
		default:
			c.Logger.Warn("?"+parts[0], "args", parts[1:])
		}
	}
	return nil
}

func (c *Client) connect(ctx context.Context) (io.ReadCloser, error) {
	form := make(url.Values)
	form.Set("LS_adapter_set", c.Set)
	form.Set("LS_cid", c.CID)
	return c.call(ctx, "create_session", form)
}

func (c *Client) processUpdate(parts []string) error {
	subID, values := parts[1], parts[3]
	sub, ok := c.subscriptions[subID]
	if !ok {
		return fmt.Errorf("invalid subscription id: %s", subID)
	}
	return sub.processUpdate(strings.Split(values, "|"))
}

func (c *Client) Subscribe(ctx context.Context, group string, schema []string, f func(Values)) error {
	if !c.Connected.Load() {
		return errors.New("client is not connected")
	}

	reqId := c.reqId.Add(1)
	subId := c.subId.Add(1)

	form := make(url.Values)
	form.Set("LS_op", "add")
	form.Set("LS_reqId", strconv.Itoa(int(reqId)))
	form.Set("LS_session", c.connectionID)
	form.Set("LS_subId", strconv.Itoa(int(subId)))
	form.Set("LS_data_adapter", "DEFAULT")
	form.Set("LS_group", group)
	form.Set("LS_schema", strings.Join(schema, " "))
	form.Set("LS_mode", "MERGE")
	form.Set("LS_requested_max_frequency", "0.1") // TODO: make this is a parameter

	r, err := c.call(ctx, "control", form)
	if err != nil {
		return err
	}

	body, _ := io.ReadAll(r)
	_ = r.Close()
	body = bytes.TrimSuffix(body, []byte("\r\n"))
	parts := strings.Split(string(body), ",")

	switch parts[0] {
	case "REQOK":
		c.subscriptions[strconv.Itoa(int(subId))] = &subscription{
			Logger:   c.Logger.With("group", group),
			callback: f,
		}
		return nil
	case "REQERR":
		return fmt.Errorf("%s (%s)", parts[3], parts[2])
	default:
		return fmt.Errorf("subscription failed: unexpected response %q", parts[0])
	}
}

func (c *Client) call(ctx context.Context, request string, values url.Values) (io.ReadCloser, error) {
	args := make(url.Values)
	args.Set("LS_protocol", "TLCP-2.0.0")

	serverURL := cmp.Or(c.serverURL, ServerURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/"+request+".txt?"+args.Encode(), strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("http: %s", resp.Status)
	}

	return resp.Body, nil
}

type subscription struct {
	Logger   *slog.Logger
	callback func(Values)
	Values   Values
}

func (s *subscription) processUpdate(update Values) (err error) {
	if s.Values, err = s.Values.Update(update); err == nil {
		if s.callback != nil {
			s.callback(s.Values)
		}
	}
	return err
}
