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
	case "setup":
		return runSetup(args[1:], stdout, stderr)
	case "start":
		return runServe(ctx, append([]string{"--http-mcp"}, args[1:]...), stdout, stderr)
	case "status":
		return runDaemonStatus(args[1:], stdout, stderr)
	case "connect":
		return runConnect(args[1:], stdout, stderr)
	case "auth":
		return runAuthCommand(args[1:], stdout, stderr)
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
		return runConnect(args[1:], stdout, stderr)
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
	addr := fs.String("addr", configuredAddr(), "HTTP listen address")
	dbPath := fs.String("db", configuredDBPath(), "SQLite database path")
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
		return runDaemonStatus(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown daemon command %q", args[0])
	}
}

func runDaemonStatus(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", configuredAddr(), "daemon address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resp, err := http.Get("http://" + *addr + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(stdout, resp.Body)
	return nil
}

func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", configuredAddr(), "HTTP listen address")
	dbPath := fs.String("db", configuredDBPath(), "SQLite database path")
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
	fmt.Fprintf(stdout, "auth_policy: %s\n", cfg.Auth.Name())
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
		fmt.Fprintf(stdout, "# HTTP MCP requires Authorization: Bearer <TUSKBASE_API_KEY>.\n")
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
	Auth       daemon.AuthPolicy
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
	authPolicy, err := loadAuthPolicy(httpMCP || rest)
	if err != nil {
		return runtimeConfig{}, err
	}
	return runtimeConfig{Addr: addr, DBPath: dbPath, HTTPMCP: httpMCP, REST: rest, Embeddings: emb, Auth: authPolicy}, nil
}

func newDaemon(ctx context.Context, cfg runtimeConfig) (*daemon.TuskbaseDaemon, error) {
	logger := slog.Default()
	return daemon.New(ctx, daemon.Config{Addr: cfg.Addr, EnableMCP: cfg.HTTPMCP, EnableREST: cfg.REST, Version: version, Logger: logger}, daemon.SQLiteStoreFactory{Path: cfg.DBPath, Embedding: cfg.Embeddings, Logger: logger}, cfg.Auth)
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

func loadAuthPolicy(required bool) (daemon.AuthPolicy, error) {
	sharedKeys := strings.TrimSpace(os.Getenv("TUSKBASE_AGENT_KEYS"))
	if sharedKeys != "" {
		keys, err := daemon.ParseLocalSharedKeys(sharedKeys)
		if err != nil {
			return nil, err
		}
		return daemon.NewLocalSharedKeyPolicy(keys)
	}
	key := strings.TrimSpace(os.Getenv("TUSKBASE_API_KEY"))
	if key != "" {
		return daemon.NewLocalAPIKeyPolicy(key)
	}
	if cfg, found, err := loadUserConfig(); err != nil {
		return nil, err
	} else if found {
		if len(cfg.AgentKeys) > 0 {
			return daemon.NewLocalSharedKeyPolicy(cfg.AgentKeys)
		}
		if strings.TrimSpace(cfg.APIKey) != "" {
			return daemon.NewLocalAPIKeyPolicy(cfg.APIKey)
		}
	}
	if required {
		return nil, errors.New("HTTP MCP and REST require auth; run `tuskbase setup` or set TUSKBASE_API_KEY")
	}
	return daemon.NoAuthPolicy{}, nil
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

func configuredAddr() string {
	if value := strings.TrimSpace(os.Getenv("TUSKBASE_ADDR")); value != "" {
		return value
	}
	if cfg, found, err := loadUserConfig(); err == nil && found && strings.TrimSpace(cfg.Addr) != "" {
		return cfg.Addr
	}
	return defaultAddr
}

func configuredDBPath() string {
	if value := strings.TrimSpace(os.Getenv("TUSKBASE_DB_PATH")); value != "" {
		return value
	}
	if cfg, found, err := loadUserConfig(); err == nil && found && strings.TrimSpace(cfg.DBPath) != "" {
		return cfg.DBPath
	}
	return defaultDBPath()
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
	fmt.Fprint(w, `Run the guided setup:

  tuskbase setup

Recommended first path: Local Basic.
It generates a local secret, stores it privately, and prepares Tuskbase for Codex, Claude Code, or Cursor.

Advanced paths:
  tuskbase setup --mode demo
  tuskbase setup --mode local-shared
`)
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Usage: tuskbase <command>

Commands:
  setup             Set up Tuskbase and generate local auth
  start             Start the local Tuskbase daemon
  status            Check whether Tuskbase is running
  connect [client]  Print MCP setup for codex, claude, cursor, or generic
  doctor            Check local setup
  version           Print version info

Advanced:
  serve             Run stdio MCP directly
  serve --http-mcp  Run HTTP MCP directly
  auth show         Show auth status; use --reveal to print secrets

Compatibility:
  init                    Alias for setup guidance
  init-mcp [client]       Alias for connect-style MCP config output
  daemon start            Alias for start
  daemon status           Alias for status
  -mode mcp|http|both     Legacy mode flag

Environment:
  TUSKBASE_CONFIG_PATH    Override the Tuskbase config file path
  TUSKBASE_API_KEY        Manual bearer key for HTTP MCP and REST
  TUSKBASE_AGENT_KEYS     Local Shared keys as name:role:key,name:role:key
`)
}
