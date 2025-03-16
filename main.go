package main

import (
	"context"
	"errors"
	"flag"
	"github.com/clambin/iss-exporter/internal/collector"
	"github.com/clambin/iss-exporter/internal/health"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

var (
	version    = "change-me"
	addr       = flag.String("addr", ":9090", "prometheus metrics address")
	healthAddr = flag.String("health", ":8080", "prometheus metrics address")
	debug      = flag.Bool("debug", false, "log debug messages")
)

func main() {
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var opts slog.HandlerOptions
	if *debug {
		opts.Level = slog.LevelDebug
	}
	l := slog.New(slog.NewTextHandler(os.Stderr, &opts))
	l.Info("Starting iss-exporter", "version", version)

	c, err := collector.NewCollector(ctx, l)
	if err != nil {
		panic(err)
	}
	prometheus.MustRegister(c)

	go func() {
		s := http.Server{
			Addr:    *healthAddr,
			Handler: health.Handler(c.ClientSession),
		}
		if err := s.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			panic(err)
		}
	}()

	http.Handle("/metrics", promhttp.Handler())
	go func() {
		if err = http.ListenAndServe(*addr, nil); !errors.Is(err, http.ErrServerClosed) {
			panic(err)
		}
	}()

	<-ctx.Done()
}
