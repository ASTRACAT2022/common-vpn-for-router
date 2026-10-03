package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"common-vpn-router/internal/api"
	"common-vpn-router/internal/config"
	"common-vpn-router/internal/storage"
	"common-vpn-router/internal/subscription"
	"common-vpn-router/internal/xray"
)

var version = "dev"

func main() {
	cfg := config.Defaults(version)
	flag.StringVar(&cfg.ListenAddress, "listen", cfg.ListenAddress, "HTTP API listen address (default is loopback only)")
	flag.StringVar(&cfg.ConfigDir, "config-dir", cfg.ConfigDir, "configuration directory")
	flag.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "application data directory")
	flag.StringVar(&cfg.XrayBinary, "xray", cfg.XrayBinary, "Xray executable path")
	flag.BoolVar(&cfg.Tunnel, "tun", cfg.Tunnel, "route router traffic through the Xray TUN interface")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	store, err := storage.Open(cfg.StatePath())
	if err != nil {
		logger.Error("could not open application state", "component", "storage", "error", err.Error())
		os.Exit(1)
	}
	controller := xray.NewController(cfg.XrayBinary, cfg.XrayConfigPath())
	if store.Snapshot().VPNEnabled {
		if err := controller.Start(); err != nil {
			logger.Error("could not restore VPN after startup", "component", "xray", "error", err.Error())
		}
	}
	server := api.NewServer(cfg, store, subscription.NewManager(store), controller, logger)
	httpServer := &http.Server{
		Addr: cfg.ListenAddress, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveResult := make(chan error, 1)
	go func() { serveResult <- httpServer.ListenAndServe() }()
	go server.RunAutoMonitor(ctx)
	go server.RunSubscriptionMonitor(ctx)
	logger.Info("daemon started", "component", "api", "address", cfg.ListenAddress, "version", version)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		if err := controller.Stop(); err != nil {
			logger.Error("could not stop Xray", "component", "xray", "error", err.Error())
		}
	case err := <-serveResult:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server stopped", "component", "api", "error", err.Error())
			os.Exit(1)
		}
	}
}
