package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/priyavratuniyal/tuskbase/internal/adapters/embeddings"
	"github.com/priyavratuniyal/tuskbase/internal/daemon"
	"github.com/priyavratuniyal/tuskbase/internal/ports"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		slog.Error("tuskbase stopped", "error", err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}
	if strings.HasPrefix(args[0], "-") {
		return runLegacy(ctx, args, stdout, stderr)
	}
	switch args[0] {
	case "version":
		printVersion(stdout)
		return nil
	case "serve":
		return runServe(ctx, args[1:], stdout, stderr)
	case "daemon":
		return runDaemonCommand(ctx, args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(ctx, args[1:], stdout, stderr)
	case "init":
		printInit(stdout)
		return nil
	case "init-mcp":
		return runInitMCP(args[1:], stdout)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// runServe is the product front door: no flags means Demo stdio MCP, while --http-mcp turns the same core into the Local Basic daemon.
func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	httpMCP := fs.Bool("http-mcp", false, "serve MCP over loopback HTTP at /mcp")
	rest := fs.Bool("rest", false, "also serve REST HTTP endpoints")
	addr := fs.String("addr", envOrDefault("TUSKBASE_ADDR", "127.0.0.1:8765"), "HTTP listen address")
	dbPath := fs.String("db", envOrDefault("TUSKBASE_DB_PATH", defaultDBPath()), "SQLite database path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadRuntimeConfig(*addr, *dbPath, *httpMCP, *rest)
	if err != nil {
		return err
	}
	d, err := newDaemon(ctx, cfg)
	if err != nil {
		return err
	}
	defer d.Close()
	if *httpMCP || *rest {
		return d.RunHTTP(ctx)
	}
	return d.RunStdio(ctx)
}

func runDaemonCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprintln(stdout, "Usage: tuskbase daemon <start|status>")
		return nil
	}
	switch args[0] {
	case "start":
		return runServe(ctx, append([]string{"--http-mcp"}, args[1:]...), stdout, stderr)
	case "status":
		fs := flag.NewFlagSet("daemon status", flag.ContinueOnError)
		fs.SetOutput(stderr)
		addr := fs.String("addr", envOrDefault("TUSKBASE_ADDR", "127.0.0.1:8765"), "daemon address")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		resp, err := http.Get("http://" + *addr + "/healthz")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(stdout, resp.Body)
		return nil
	default:
		return fmt.Errorf("unknown daemon command %q", args[0])
	}
}

func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", envOrDefault("TUSKBASE_ADDR", "127.0.0.1:8765"), "HTTP listen address")
	dbPath := fs.String("db", envOrDefault("TUSKBASE_DB_PATH", defaultDBPath()), "SQLite database path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadRuntimeConfig(*addr, *dbPath, true, false)
	if err != nil {
		return err
	}
	d, err := newDaemon(ctx, cfg)
	if err != nil {
		return err
	}
	defer d.Close()
	fmt.Fprintf(stdout, "tuskbase: ok\n")
	fmt.Fprintf(stdout, "version: %s\n", version)
	fmt.Fprintf(stdout, "store: sqlite\n")
	fmt.Fprintf(stdout, "db_path: %s\n", *dbPath)
	fmt.Fprintf(stdout, "addr: %s\n", *addr)
	fmt.Fprintf(stdout, "mcp: ok\n")
	if strings.EqualFold(os.Getenv("TUSKBASE_EMBEDDING_PROVIDER"), "openai") && strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		fmt.Fprintf(stdout, "openai: missing OPENAI_API_KEY\n")
	} else {
		fmt.Fprintf(stdout, "openai: ok or disabled\n")
	}
	return nil
}

func runInitMCP(args []string, stdout io.Writer) error {
	client := "generic"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		client = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("init-mcp", flag.ContinueOnError)
	mode := fs.String("mode", "demo", "demo or local-basic")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		client = fs.Arg(0)
	}
	exe, err := os.Executable()
	if err != nil {
		exe = "tuskbase"
	}
	switch strings.ToLower(*mode) {
	case "demo":
		fmt.Fprintf(stdout, "# %s MCP config for Tuskbase Demo\n", client)
		fmt.Fprintf(stdout, "[mcp_servers.tuskbase]\ncommand = %q\nargs = [\"serve\"]\n", exe)
	case "local-basic":
		fmt.Fprintf(stdout, "# %s MCP config for Tuskbase Local Basic\n", client)
		fmt.Fprintf(stdout, "[mcp_servers.tuskbase]\nurl = \"http://127.0.0.1:8765/mcp\"\n")
	default:
		return fmt.Errorf("unknown MCP mode %q", *mode)
	}
	return nil
}

func runLegacy(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("legacy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "http", "server mode: http, mcp, or both")
	if err := fs.Parse(args); err != nil {
		return err
	}
	serveArgs, err := legacyServeArgs(*mode)
	if err != nil {
		return err
	}
	return runServe(ctx, serveArgs, stdout, stderr)
}

func legacyServeArgs(mode string) ([]string, error) {
	switch mode {
	case "mcp":
		return nil, nil
	case "http":
		return []string{"--rest"}, nil
	case "both":
		return []string{"--http-mcp", "--rest"}, nil
	default:
		return nil, fmt.Errorf("unknown mode %q", mode)
	}
}

type runtimeConfig struct {
	Addr       string
	DBPath     string
	HTTPMCP    bool
	REST       bool
	Embeddings ports.EmbeddingProvider
}

func loadRuntimeConfig(addr, dbPath string, httpMCP, rest bool) (runtimeConfig, error) {
	if httpMCP || rest {
		if err := requireLoopback(addr); err != nil {
			return runtimeConfig{}, err
		}
	}
	emb, err := loadEmbeddingProvider()
	if err != nil {
		return runtimeConfig{}, err
	}
	return runtimeConfig{Addr: addr, DBPath: dbPath, HTTPMCP: httpMCP, REST: rest, Embeddings: emb}, nil
}

func newDaemon(ctx context.Context, cfg runtimeConfig) (*daemon.TuskbaseDaemon, error) {
	logger := slog.Default()
	return daemon.New(ctx, daemon.Config{Addr: cfg.Addr, EnableMCP: cfg.HTTPMCP, EnableREST: cfg.REST, Version: version, Logger: logger}, daemon.SQLiteStoreFactory{Path: cfg.DBPath, Embedding: cfg.Embeddings, Logger: logger}, daemon.NoAuthPolicy{})
}

// loadEmbeddingProvider keeps embeddings optional; text lookup must stay usable without network access or a model provider.
func loadEmbeddingProvider() (ports.EmbeddingProvider, error) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("TUSKBASE_EMBEDDING_PROVIDER")))
	switch provider {
	case "", "none", "text":
		return nil, nil
	case "openai":
		return embeddings.NewOpenAIProvider(os.Getenv("OPENAI_API_KEY"), os.Getenv("TUSKBASE_EMBEDDING_MODEL"), os.Getenv("TUSKBASE_OPENAI_BASE_URL"), nil)
	default:
		return nil, fmt.Errorf("unknown TUSKBASE_EMBEDDING_PROVIDER %q", provider)
	}
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

// requireLoopback is the Local Basic safety rail: HTTP MCP is useful locally, but should not quietly bind to the network.
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

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "tuskbase %s\ncommit: %s\ndate: %s\ngo: %s\nplatform: %s/%s\n", version, commit, date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func printInit(w io.Writer) {
	fmt.Fprint(w, `Choose your Tuskbase mode:

1. Demo
   One agent, fastest setup.
   Uses stdio MCP + SQLite.

2. Local Basic
   Multiple local agents/tools on one machine.
   Run: tuskbase daemon start
   Uses HTTP MCP daemon + SQLite.

Local Shared with Postgres is planned after these tiers.
`)
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Usage: tuskbase <command>

Commands:
  version                 Print version and build metadata
  init                    Explain Demo and Local Basic setup paths
  init-mcp [client]       Print MCP client config
  serve                   Run Demo mode: stdio MCP + SQLite
  serve --http-mcp        Run Local Basic daemon: HTTP MCP + SQLite
  daemon start            Alias for serve --http-mcp
  daemon status           Check local daemon health
  doctor                  Check local setup

Compatibility:
  -mode mcp|http|both     Legacy mode flag
`)
}
