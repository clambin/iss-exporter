package main

import (
	"context"
	"flag"
	"github.com/clambin/iss-exporter/internal/collector"
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

	groups := []string{
		"NODE3000005",   // Urine Tank Qty
		"NODE3000008",   // Waste Water Tank Qty
		"NODE3000009",   // Clean Water Tank Qty
		"USLAB000058",   // cabin pressure USA Lab
		"AIRLOCK000049", // crewlock pressure
	}

	c := collector.NewCollector(ctx, groups, slog.New(slog.NewTextHandler(os.Stderr, &opts)))
	prometheus.MustRegister(c)

	http.Handle("/metrics", promhttp.Handler())
	_ = http.ListenAndServe(*addr, nil)
}
