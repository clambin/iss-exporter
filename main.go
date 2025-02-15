package main

import (
	"context"
	"flag"
	"github.com/clambin/iss_exporter/internal/collector"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

var (
	addr  = flag.String("addr", ":9090", "prometheus metrics address")
	debug = flag.Bool("debug", false, "log debug messages")
)

func main() {
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var opts slog.HandlerOptions
	if *debug {
		opts.Level = slog.LevelDebug
	}

	c := collector.NewCollector(ctx, slog.New(slog.NewTextHandler(os.Stderr, &opts)))
	prometheus.MustRegister(c)

	http.Handle("/metrics", promhttp.Handler())
	_ = http.ListenAndServe(*addr, nil)
}
