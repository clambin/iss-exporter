package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/clambin/iss-exporter/lightstreamer"
	"github.com/prometheus/client_golang/prometheus"
	"log/slog"
	"net/http"
	"strconv"
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
	connection *lightstreamer.ClientSession
	Logger     *slog.Logger
}

func NewCollector(ctx context.Context, logger *slog.Logger) (c *Collector, err error) {
	c = &Collector{
		Logger: logger,
	}
	c.connection, err = lightStreamerClientSession(ctx, logger)
	return c, err
}

func (c Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- locationMetric
	telemetryMetric.Describe(ch)
}

func (c Collector) Collect(ch chan<- prometheus.Metric) {
	telemetryMetric.Collect(ch)

	longitude, latitude, err := getLocation()
	if err != nil {
		c.Logger.Error("failed to get location", "err", err)
		return
	}
	c.Logger.Debug("location found", "longitude", longitude, "latitude", latitude)
	ch <- prometheus.MustNewConstMetric(locationMetric, prometheus.GaugeValue, 1.0, longitude, latitude)
}

func getLocation() (string, string, error) {
	type ISSUpdate struct {
		IssPosition struct {
			Latitude  string `json:"latitude"`
			Longitude string `json:"longitude"`
		} `json:"iss_position"`
		Message   string `json:"message"`
		Timestamp int    `json:"timestamp"`
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

var groups = []string{
	"NODE3000005",   // Urine Tank Qty
	"NODE3000008",   // Waste Water Tank Qty
	"NODE3000009",   // Clean Water Tank Qty
	"NODE3000011",   // O2 production rate
	"USLAB000058",   // cabin pressure
	"USLAB000059",   // cabin temperature
	"AIRLOCK000049", // crewlock pressure
	"AIRLOCK000054", // Airlock Pressure
	"USLAB000053",   // Lab ppO2
}

var schema = []string{"Value"}

func lightStreamerClientSession(ctx context.Context, logger *slog.Logger) (*lightstreamer.ClientSession, error) {
	session, err := lightstreamer.NewClientSession(
		ctx,
		lightstreamer.WithLogger(logger),
		lightstreamer.WithAdapterSet("ISSLIVE"),
	)
	if err != nil {
		return nil, err
	}

	for _, group := range groups {
		if err = session.Subscribe(ctx, "DEFAULT", group, schema, 0.1, func(_ int, values lightstreamer.Values) {
			if values[0] == nil {
				logger.Warn("empty value in subscription. ignoring")
				return
			}
			value, err := strconv.ParseFloat(string(*values[0]), 64)
			if err != nil {
				logger.Error("failed to parse value", "group", group, "value", *values[0], "err", err)
				return
			}
			telemetryMetric.WithLabelValues(group).Set(value)
			logger.Debug("update processed", "group", group, "value", value)
		}); err != nil {
			return nil, fmt.Errorf("subscribe(%s): %w", group, err)
		}
		logger.Info("subscribed successfully", "group", group)
	}
	return session, nil
}
