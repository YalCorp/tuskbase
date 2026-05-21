package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	httpapi "github.com/priyavratuniyal/tuskbase/internal/adapters/http"
	mcpadapter "github.com/priyavratuniyal/tuskbase/internal/adapters/mcp"
	"github.com/priyavratuniyal/tuskbase/internal/adapters/sqlite"
	"github.com/priyavratuniyal/tuskbase/internal/app"
)

func main() {
	if err := run(); err != nil {
		slog.Error("tuskbase stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "http", "server mode: http, mcp, or both")
	flag.Parse()

	config, err := loadConfig()
	if err != nil {
		return err
	}
	setupLogging(config.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := sqlite.Open(ctx, config.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	service := app.NewService(store, store, app.UUIDGenerator{}, app.SystemClock{})
	httpServer := httpapi.NewServer(service)
	mcpServer := mcpadapter.NewServer(service)
	switch *mode {
	case "http":
		return runHTTP(ctx, config.Addr, httpServer)
	case "mcp":
		return mcpServer.Run(ctx, &mcp.StdioTransport{})
	case "both":
		return runBoth(ctx, config.Addr, httpServer, mcpServer)
	default:
		return fmt.Errorf("unknown mode %q", *mode)
	}
}

type config struct {
	DBPath   string
	Addr     string
	LogLevel string
}

func loadConfig() (config, error) {
	cfg := config{
		DBPath:   envOrDefault("TUSKBASE_DB_PATH", defaultDBPath()),
		Addr:     envOrDefault("TUSKBASE_ADDR", "127.0.0.1:8765"),
		LogLevel: envOrDefault("TUSKBASE_LOG_LEVEL", "info"),
	}
	if err := requireLoopback(cfg.Addr); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "tuskbase.db"
	}
	return filepath.Join(home, ".local", "share", "tuskbase", "tuskbase.db")
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid TUSKBASE_ADDR %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ips, err := net.LookupIP(host)
		if err != nil {
			return err
		}
		for _, candidate := range ips {
			if candidate.IsLoopback() {
				return nil
			}
		}
	} else if ip.IsLoopback() {
		return nil
	}
	if strings.EqualFold(os.Getenv("TUSKBASE_ALLOW_NON_LOOPBACK"), "true") {
		return nil
	}
	return errors.New("non-loopback TUSKBASE_ADDR requires TUSKBASE_ALLOW_NON_LOOPBACK=true")
}

func setupLogging(level string) {
	var slogLevel slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slogLevel})))
}

func runHTTP(ctx context.Context, addr string, handler http.Handler) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		slog.Info("starting tuskbase http server", "addr", addr)
		errc <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func runBoth(ctx context.Context, addr string, handler http.Handler, mcpServer *mcp.Server) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errc := make(chan error, 2)
	go func() {
		errc <- runHTTP(ctx, addr, handler)
	}()
	go func() {
		errc <- mcpServer.Run(ctx, &mcp.StdioTransport{})
	}()

	err := <-errc
	cancel()
	if errors.Is(err, context.Canceled) || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
