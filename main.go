package main

import (
	"encoding/json"
	"errors"
	"flag"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"log/slog"
	"net/http"
	"os"
)

var (
	addr  = flag.String("addr", ":9090", "prometheus metrics address")
	debug = flag.Bool("debug", false, "log debug messages")

	locationMetric = prometheus.NewDesc(
		prometheus.BuildFQName("iss", "", "location"),
		"current ISS location",
		[]string{"longitude", "latitude"},
		nil,
	)
)

func main() {
	flag.Parse()

	var opts slog.HandlerOptions
	if *debug {
		opts.Level = slog.LevelDebug
	}
	c := collector{Logger: slog.New(slog.NewTextHandler(os.Stderr, &opts))}
	prometheus.MustRegister(c)

	http.Handle("/metrics", promhttp.Handler())
	_ = http.ListenAndServe(*addr, nil)
}

var _ prometheus.Collector = collector{}

type collector struct {
	Logger *slog.Logger
}

func (c collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- locationMetric
}

func (c collector) Collect(ch chan<- prometheus.Metric) {
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
