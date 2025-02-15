package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/clambin/iss_exporter/lightstreamer"
	"github.com/prometheus/client_golang/prometheus"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

var (
	locationMetric = prometheus.NewDesc(
		prometheus.BuildFQName("iss", "", "location"),
		"current ISS location",
		[]string{"longitude", "latitude"},
		nil,
	)

	telemetryMetric = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace:   "iss",
		Subsystem:   "telemetry",
		Name:        "metric",
		Help:        "lightstreamer telemetry",
		ConstLabels: nil,
	}, []string{"group"})
)

type Collector struct {
	Logger *slog.Logger
}

func NewCollector(ctx context.Context, groups []string, logger *slog.Logger) *Collector {
	tc := telemetryCollector{
		set:    "ISSLIVE",
		Logger: logger,
		groups: groups,
	}
	go tc.run(ctx)
	return &Collector{Logger: logger}
}

func (c Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- locationMetric
	telemetryMetric.Describe(ch)
}

func (c Collector) Collect(ch chan<- prometheus.Metric) {
	longitude, latitude, err := getLocation()
	if err != nil {
		c.Logger.Error("failed to get location", "err", err)
		return
	}
	c.Logger.Debug("location found", "longitude", longitude, "latitude", latitude)
	ch <- prometheus.MustNewConstMetric(locationMetric, prometheus.GaugeValue, 1.0, longitude, latitude)
	telemetryMetric.Collect(ch)
}

func getLocation() (string, string, error) {
	type ISSUpdate struct {
		Timestamp   int `json:"timestamp"`
		IssPosition struct {
			Latitude  string `json:"latitude"`
			Longitude string `json:"longitude"`
		} `json:"iss_position"`
		Message string `json:"message"`
	}
	resp, err := http.Get("http://api.open-notify.org/iss-now.json")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", errors.New(resp.Status)
	}
	var update ISSUpdate
	err = json.NewDecoder(resp.Body).Decode(&update)
	return update.IssPosition.Longitude, update.IssPosition.Latitude, err
}

type telemetryCollector struct {
	set    string
	groups []string
	Logger *slog.Logger
}

var schema = []string{"Value"}

func (t *telemetryCollector) run(ctx context.Context) {
	var ch chan error
	c := lightstreamer.NewClient(t.set, "mgQkwtwdysogQz2BJ4Ji%20kOj2Bg", t.Logger.With("lightstreamer", t.set))

	for {
		if ch == nil {
			ch = t.connect(ctx, c)
		}
		select {
		case <-ctx.Done():
			return
		case err := <-ch:
			t.Logger.Warn("lightstreamer error", "err", err)
			ch = nil
		}
	}
}

func (t *telemetryCollector) connect(ctx context.Context, c *lightstreamer.Client) chan error {
	ch := make(chan error)
	go func() { ch <- c.Run(ctx) }()
	for !c.Connected.Load() {
		time.Sleep(100 * time.Millisecond)
	}
	if err := t.subscribe(ctx, c); err != nil {
		t.Logger.Error("failed to subscribe to lightstreamer", "err", err)
	}
	return ch
}

func (t *telemetryCollector) subscribe(ctx context.Context, c *lightstreamer.Client) error {
	for _, group := range t.groups {
		if err := c.Subscribe(ctx, group, schema, func(values lightstreamer.Values) {
			t.Logger.Debug("lightstreamer", "group", group, "values", values)
			value, err := strconv.ParseFloat(values[0], 64)
			if err != nil {
				t.Logger.Error("failed to parse value", "group", group, "value", values[0], "err", err)
				return
			}
			telemetryMetric.WithLabelValues(group).Set(value)
		}); err != nil {
			return fmt.Errorf("subscribe(%s): %w", "NODE3000005", err)
		}
	}
	return nil
}
