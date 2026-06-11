package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	_ "github.com/jackc/pgx/v5/stdlib"

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
	case "bridge":
		return runBridge(ctx, args[1:], stdout, stderr)
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
		fmt.Fprintln(stdout, "Usage: tuskbase daemon <start|status|install|uninstall|restart>")
		return nil
	}
	switch args[0] {
	case "start":
		return runServe(ctx, append([]string{"--http-mcp"}, args[1:]...), stdout, stderr)
	case "status":
		return runDaemonStatus(args[1:], stdout, stderr)
	case "install":
		cfg, err := daemonCommandConfig(args[1:], stderr)
		if err != nil {
			return err
		}
		return printDaemonLifecycleResult(stdout, "install", newLifecycleController().InstallAndStart(ctx, cfg))
	case "uninstall":
		cfg, err := daemonCommandConfig(args[1:], stderr)
		if err != nil {
			return err
		}
		return printDaemonLifecycleResult(stdout, "uninstall", newLifecycleController().Uninstall(ctx, cfg))
	case "restart":
		cfg, err := daemonCommandConfig(args[1:], stderr)
		if err != nil {
			return err
		}
		return printDaemonLifecycleResult(stdout, "restart", newLifecycleController().Restart(ctx, cfg))
	default:
		return fmt.Errorf("unknown daemon command %q", args[0])
	}
}

func printDaemonLifecycleResult(stdout io.Writer, action string, result lifecycleResult) error {
	printLifecycleResult(stdout, "service", result)
	if result.Err != nil {
		return fmt.Errorf("daemon %s: %w", action, result.Err)
	}
	if result.Degraded {
		detail := result.Detail
		if strings.TrimSpace(detail) == "" {
			detail = emptyDefault(result.State, "degraded")
		}
		return fmt.Errorf("daemon %s degraded: %s", action, detail)
	}
	return nil
}

func daemonCommandConfig(args []string, stderr io.Writer) (userConfig, error) {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", configuredAddr(), "daemon address")
	dbPath := fs.String("db", configuredDBPath(), "SQLite database path")
	if err := fs.Parse(args); err != nil {
		return userConfig{}, err
	}
	cfg, found, err := loadUserConfig()
	if err != nil {
		return userConfig{}, err
	}
	if !found {
		return userConfig{}, errors.New("no Tuskbase setup found; run `tuskbase setup` first")
	}
	cfg.Addr = *addr
	cfg.DBPath = *dbPath
	return normalizedDaemonConfig(cfg), nil
}

func runDaemonStatus(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", configuredAddr(), "daemon address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, found, err := loadUserConfig()
	if err != nil {
		return err
	}
	if !found {
		cfg = userConfig{Mode: modeLocalBasic, Addr: *addr, DBPath: configuredDBPath()}
	}
	cfg.Addr = *addr
	printLifecycleStatus(stdout, newLifecycleController().Status(context.Background(), normalizedDaemonConfig(cfg)))
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
	cfg, found, err := loadUserConfig()
	if err != nil {
		return err
	}
	if !found {
		cfg = userConfig{Mode: modeLocalBasic}
	}
	cfg.Addr = *addr
	cfg.DBPath = *dbPath
	cfg = normalizedDaemonConfig(cfg)
	store, storeErr := loadRuntimeStoreConfig(cfg.DBPath)
	storeCheck := storeRuntimeCheck{}
	if storeErr == nil {
		storeCheck = checkRuntimeStore(ctx, cfg, store)
	}
	incompleteLocalShared := cfg.Mode == modeLocalShared && strings.TrimSpace(store.PostgresDSN) == ""
	authPolicy, authErr := loadAuthPolicy(cfg.Mode != modeDemo)
	status := newLifecycleController().Status(ctx, cfg)
	fmt.Fprintf(stdout, "tuskbase: ok\n")
	fmt.Fprintf(stdout, "version: %s\n", version)
	fmt.Fprintf(stdout, "store: %s\n", emptyDefault(store.Type, "unavailable"))
	if incompleteLocalShared {
		fmt.Fprintf(stdout, "setup_state: incomplete\n")
		fmt.Fprintf(stdout, "repair_hint: Local Shared config is missing Postgres settings; older tuskbase binaries may have written auth-only Local Shared config. Update or reinstall tuskbase, then rerun `tuskbase setup --mode local-shared --yes`, or use `--postgres-source existing --postgres-dsn <dsn>`.\n")
	}
	if store.Type == storeSQLite {
		fmt.Fprintf(stdout, "db_path: %s\n", store.SQLitePath)
	}
	if store.Type == storePostgres {
		fmt.Fprintf(stdout, "postgres_driver: %s\n", store.PostgresDriver)
		fmt.Fprintf(stdout, "postgres_dsn: %s\n", secretStatus(store.PostgresDSN))
		if cfg.Store.Postgres != nil && strings.TrimSpace(cfg.Store.Postgres.Source) != "" {
			fmt.Fprintf(stdout, "postgres_source: %s\n", cfg.Store.Postgres.Source)
		}
		if cfg.Store.Postgres != nil && cfg.Store.Postgres.Docker != nil {
			docker := cfg.Store.Postgres.Docker
			if strings.TrimSpace(docker.Context) != "" {
				fmt.Fprintf(stdout, "docker_context: %s\n", docker.Context)
			}
			fmt.Fprintf(stdout, "docker_postgres_image: %s\n", docker.Image)
			fmt.Fprintf(stdout, "docker_postgres_port: %d\n", docker.Port)
		}
		if storeCheck.Checked {
			if storeCheck.Ready {
				fmt.Fprintf(stdout, "store_runtime: ready\n")
				fmt.Fprintf(stdout, "postgres_connect: ok\n")
			} else {
				fmt.Fprintf(stdout, "store_runtime: not-ready\n")
				fmt.Fprintf(stdout, "postgres_connect: %s\n", storeCheck.Status)
				fmt.Fprintf(stdout, "postgres_error: %s\n", storeCheck.Error)
				if storeCheck.RepairHint != "" {
					fmt.Fprintf(stdout, "repair_hint: %s\n", storeCheck.RepairHint)
				}
				if storeCheck.FallbackHint != "" {
					fmt.Fprintf(stdout, "fallback_hint: %s\n", storeCheck.FallbackHint)
				}
			}
		}
	}
	if storeErr != nil {
		fmt.Fprintf(stdout, "store_error: %s\n", storeErr)
	}
	fmt.Fprintf(stdout, "addr: %s\n", cfg.Addr)
	if status.Health != nil {
		fmt.Fprintf(stdout, "mcp: ready\n")
	} else {
		fmt.Fprintf(stdout, "mcp: not-ready (%v)\n", status.HealthError)
	}
	if authErr != nil {
		fmt.Fprintf(stdout, "auth_policy: unavailable\n")
		fmt.Fprintf(stdout, "auth_error: %s\n", authErr)
	} else {
		fmt.Fprintf(stdout, "auth_policy: %s\n", authPolicy.Name())
		fmt.Fprintf(stdout, "auth_source: %s\n", authPolicy.Source())
	}
	fmt.Fprintf(stdout, "service_backend: %s\n", status.Backend)
	fmt.Fprintf(stdout, "service_state: %s\n", emptyDefault(status.State, "unknown"))
	fmt.Fprintf(stdout, "service_autostart: %s\n", emptyDefault(status.Autostart, "unknown"))
	fmt.Fprintf(stdout, "log_path: %s\n", status.LogPath)
	if strings.EqualFold(os.Getenv("TUSKBASE_EMBEDDING_PROVIDER"), "openai") && strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		fmt.Fprintf(stdout, "openai: missing OPENAI_API_KEY\n")
	} else {
		fmt.Fprintf(stdout, "openai: ok or disabled\n")
	}
	fmt.Fprintf(stdout, "clients: codex, claude, cursor, generic (print with `tuskbase connect <client>`)\n")
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
	Store      runtimeStoreConfig
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
	store, err := loadRuntimeStoreConfig(dbPath)
	if err != nil {
		return runtimeConfig{}, err
	}
	emb, err := loadEmbeddingProvider()
	if err != nil {
		return runtimeConfig{}, err
	}
	authPolicy, err := loadAuthPolicy(httpMCP || rest)
	if err != nil {
		return runtimeConfig{}, err
	}
	return runtimeConfig{Addr: addr, DBPath: dbPath, Store: store, HTTPMCP: httpMCP, REST: rest, Embeddings: emb, Auth: authPolicy}, nil
}

func newDaemon(ctx context.Context, cfg runtimeConfig) (*daemon.TuskbaseDaemon, error) {
	logger := slog.Default()
	factory, err := storeFactoryForRuntime(cfg, logger)
	if err != nil {
		return nil, err
	}
	return daemon.New(ctx, daemon.Config{Addr: cfg.Addr, EnableMCP: cfg.HTTPMCP, EnableREST: cfg.REST, Version: version, Logger: logger}, factory, cfg.Auth)
}

func storeFactoryForRuntime(cfg runtimeConfig, logger *slog.Logger) (daemon.StoreFactory, error) {
	switch cfg.Store.Type {
	case "", storeSQLite:
		path := cfg.Store.SQLitePath
		if strings.TrimSpace(path) == "" {
			path = cfg.DBPath
		}
		return daemon.SQLiteStoreFactory{Path: path, Embedding: cfg.Embeddings, Logger: logger}, nil
	case storePostgres:
		return daemon.PostgresStoreFactory{DriverName: cfg.Store.PostgresDriver, DSN: cfg.Store.PostgresDSN, Embedding: cfg.Embeddings, Logger: logger}, nil
	default:
		return nil, fmt.Errorf("unknown store %q", cfg.Store.Type)
	}
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
		return daemon.NewLocalSharedKeyPolicyWithSource(keys, "env:TUSKBASE_AGENT_KEYS")
	}
	key := strings.TrimSpace(os.Getenv("TUSKBASE_API_KEY"))
	if key != "" {
		return daemon.NewLocalAPIKeyPolicyWithSource(key, "env:TUSKBASE_API_KEY")
	}
	if cfg, found, err := loadUserConfig(); err != nil {
		return nil, err
	} else if found {
		if cfg.HasAuth() {
			return daemon.NewDynamicAuthPolicy("config", func(ctx context.Context) (daemon.AuthPolicy, error) {
				latest, found, err := loadUserConfig()
				if err != nil {
					return nil, err
				}
				if !found {
					return nil, errors.New("Tuskbase config disappeared; run `tuskbase setup`")
				}
				return authPolicyFromConfig(latest)
			})
		}
	}
	if required {
		return nil, errors.New("HTTP MCP and REST require auth; run `tuskbase setup` or set TUSKBASE_API_KEY")
	}
	return daemon.NoAuthPolicy{}, nil
}

func authPolicyFromConfig(cfg userConfig) (daemon.AuthPolicy, error) {
	if len(cfg.AgentKeys) > 0 {
		return daemon.NewLocalSharedKeyPolicyWithSource(cfg.AgentKeys, "config")
	}
	if strings.TrimSpace(cfg.APIKey) != "" {
		return daemon.NewLocalAPIKeyPolicyWithSource(cfg.APIKey, "config")
	}
	return nil, errors.New("Tuskbase config has no auth keys; run `tuskbase setup`")
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
  bridge            Run stdio MCP bridge with Tuskbase-managed local auth
  doctor            Check local setup
  version           Print version info

Advanced:
  serve             Run stdio MCP directly
  serve --http-mcp  Run HTTP MCP directly
  auth list         Show local auth keys; use --reveal to print secrets
  auth rotate       Rotate Local Basic or Local Shared keys
  auth add/remove   Manage Local Shared named keys

Compatibility:
  init                    Alias for setup guidance
  init-mcp [client]       Alias for connect-style MCP config output
  daemon start            Alias for start
  daemon status           Alias for status
  daemon install          Install and start user-scope autostart
  daemon restart          Restart the user-scope daemon service
  daemon uninstall        Remove user-scope autostart
  -mode mcp|http|both     Legacy mode flag

Environment:
  TUSKBASE_CONFIG_PATH    Override the Tuskbase config file path
  TUSKBASE_API_KEY        Manual bearer key for HTTP MCP and REST
  TUSKBASE_AGENT_KEYS     Local Shared keys as name:role:key,name:role:key
  TUSKBASE_ADDR           Override the local daemon listen address
  TUSKBASE_DB_PATH        Override the SQLite database path
  TUSKBASE_STORE          Durable store override: sqlite or postgres
  TUSKBASE_POSTGRES_DSN   Postgres DSN for Local Shared
  TUSKBASE_POSTGRES_DRIVER Postgres database/sql driver; defaults to pgx
  TUSKBASE_DOCKER_CONTEXT Docker context for Local Shared Docker setup; use auto to opt into desktop-linux fallback
`)
}
