package util

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
)

func DumpRequest(l *slog.Logger, req *http.Request) {
	var body bytes.Buffer
	tee := io.TeeReader(req.Body, &body)
	payload, _ := io.ReadAll(tee)
	req.Body = io.NopCloser(&body)
	l.Info("request", "url", req.URL.String(), "headers", req.Header, "body", string(payload))
}

func DumpResponse(l *slog.Logger, resp *http.Response) {
	//payload, _ := io.ReadAll(resp.Body)
	//_ = resp.Body.Close()
	l.Info("response", "headers", resp.Header)
	//resp.Body = io.NopCloser(bytes.NewBuffer(payload))
}

var _ http.RoundTripper = &LoggingRoundTripper{}

type LoggingRoundTripper struct {
	Next   http.RoundTripper
	Logger *slog.Logger
}

func (l LoggingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	DumpRequest(l.Logger, request)
	resp, err := l.Next.RoundTrip(request)
	DumpResponse(l.Logger, resp)
	return resp, err
}
