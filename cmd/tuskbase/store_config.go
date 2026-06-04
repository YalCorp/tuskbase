package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	storeSQLite            = "sqlite"
	storePostgres          = "postgres"
	defaultPostgresDriver  = "pgx"
	postgresSourceAuto     = "auto"
	postgresSourceDocker   = "docker"
	postgresSourceExisting = "existing"
	postgresSourceSupabase = "supabase"
)

type storeConfig struct {
	Type     string               `json:"type,omitempty"`
	Postgres *postgresStoreConfig `json:"postgres,omitempty"`
}

type postgresStoreConfig struct {
	Source string                `json:"source,omitempty"`
	Driver string                `json:"driver,omitempty"`
	DSN    string                `json:"dsn,omitempty"`
	Docker *dockerPostgresConfig `json:"docker,omitempty"`
}

type dockerPostgresConfig struct {
	Project     string `json:"project,omitempty"`
	ComposePath string `json:"compose_path,omitempty"`
	Service     string `json:"service,omitempty"`
	Image       string `json:"image,omitempty"`
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
	Database    string `json:"database,omitempty"`
	User        string `json:"user,omitempty"`
	Volume      string `json:"volume,omitempty"`
}

type runtimeStoreConfig struct {
	Type           string
	SQLitePath     string
	PostgresDriver string
	PostgresDSN    string
}

type setupStoreOptions struct {
	PostgresDSN         string
	PostgresDriver      string
	PostgresSource      string
	DockerPostgresPort  int
	DockerPostgresImage string
	PrintOnly           bool
	ConfigPath          string
}

type setupStoreResult struct {
	DockerPostgres *dockerPostgresProvisionResult
}

func applySetupStoreConfig(cfg *userConfig, opts setupStoreOptions) (setupStoreResult, error) {
	switch cfg.Mode {
	case modeDemo, modeLocalBasic:
		cfg.Store = storeConfig{Type: storeSQLite}
		return setupStoreResult{}, nil
	case modeLocalShared:
		pg := postgresConfigForSetup(cfg.Store.Postgres, opts.PostgresDSN, opts.PostgresDriver)
		source, err := resolvePostgresSource(opts.PostgresSource, cfg.Store.Postgres, pg, opts.PostgresDSN)
		if err != nil {
			return setupStoreResult{}, err
		}
		pg.Source = source
		switch source {
		case postgresSourceDocker:
			provisioned, err := provisionDockerPostgresForSetup(pg, opts)
			if err != nil {
				return setupStoreResult{}, err
			}
			pg.DSN = provisioned.DSN
			pg.Docker = &provisioned.Config
			cfg.Store = storeConfig{Type: storePostgres, Postgres: &pg}
			return setupStoreResult{DockerPostgres: &provisioned}, nil
		case postgresSourceExisting, postgresSourceSupabase:
			if strings.TrimSpace(pg.DSN) == "" {
				return setupStoreResult{}, fmt.Errorf("postgres dsn is required for Local Shared source %q; pass --postgres-dsn or set TUSKBASE_POSTGRES_DSN", source)
			}
			pg.Docker = nil
		default:
			return setupStoreResult{}, fmt.Errorf("unsupported postgres source %q", source)
		}
		cfg.Store = storeConfig{Type: storePostgres, Postgres: &pg}
		return setupStoreResult{}, nil
	default:
		return setupStoreResult{}, fmt.Errorf("unsupported setup mode %q", cfg.Mode)
	}
}

func postgresConfigForSetup(existing *postgresStoreConfig, dsnFlag, driverFlag string) postgresStoreConfig {
	pg := postgresStoreConfig{}
	if existing != nil {
		pg = *existing
	}
	if dsn := strings.TrimSpace(dsnFlag); dsn != "" {
		pg.DSN = dsn
	} else if strings.TrimSpace(pg.DSN) == "" {
		pg.DSN = strings.TrimSpace(os.Getenv("TUSKBASE_POSTGRES_DSN"))
	}
	if driver := strings.TrimSpace(driverFlag); driver != "" {
		pg.Driver = driver
	} else if strings.TrimSpace(pg.Driver) == "" {
		pg.Driver = strings.TrimSpace(os.Getenv("TUSKBASE_POSTGRES_DRIVER"))
	}
	if strings.TrimSpace(pg.Driver) == "" {
		pg.Driver = defaultPostgresDriver
	}
	return pg
}

func resolvePostgresSource(sourceFlag string, existing *postgresStoreConfig, pg postgresStoreConfig, dsnFlag string) (string, error) {
	source, err := normalizePostgresSource(sourceFlag)
	if err != nil {
		return "", err
	}
	if source != postgresSourceAuto {
		return source, nil
	}
	if strings.TrimSpace(dsnFlag) != "" || strings.TrimSpace(os.Getenv("TUSKBASE_POSTGRES_DSN")) != "" {
		if existing != nil && existing.Source == postgresSourceSupabase {
			return postgresSourceSupabase, nil
		}
		return postgresSourceExisting, nil
	}
	if existing != nil && strings.TrimSpace(pg.DSN) != "" {
		switch existing.Source {
		case postgresSourceDocker, postgresSourceExisting, postgresSourceSupabase:
			return existing.Source, nil
		default:
			return postgresSourceExisting, nil
		}
	}
	return postgresSourceDocker, nil
}

func normalizePostgresSource(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", postgresSourceAuto:
		return postgresSourceAuto, nil
	case postgresSourceDocker:
		return postgresSourceDocker, nil
	case postgresSourceExisting, "postgres":
		return postgresSourceExisting, nil
	case postgresSourceSupabase:
		return postgresSourceSupabase, nil
	default:
		return "", fmt.Errorf("unknown postgres source %q; expected auto, docker, existing, or supabase", value)
	}
}

func loadRuntimeStoreConfig(dbPath string) (runtimeStoreConfig, error) {
	store := runtimeStoreConfig{Type: storeSQLite, SQLitePath: dbPath, PostgresDriver: defaultPostgresDriver}
	if cfg, found, err := loadUserConfig(); err != nil {
		return runtimeStoreConfig{}, err
	} else if found {
		store.Type = defaultStoreTypeForMode(cfg.Mode)
		if cfg.Store.Type != "" {
			store.Type = cfg.Store.Type
		}
		if cfg.Store.Postgres != nil {
			store.PostgresDriver = cfg.Store.Postgres.Driver
			store.PostgresDSN = cfg.Store.Postgres.DSN
		}
	}
	if envStore := strings.TrimSpace(os.Getenv("TUSKBASE_STORE")); envStore != "" {
		store.Type = envStore
	}
	if envDriver := strings.TrimSpace(os.Getenv("TUSKBASE_POSTGRES_DRIVER")); envDriver != "" {
		store.PostgresDriver = envDriver
	}
	if envDSN := strings.TrimSpace(os.Getenv("TUSKBASE_POSTGRES_DSN")); envDSN != "" {
		store.PostgresDSN = envDSN
		if strings.TrimSpace(os.Getenv("TUSKBASE_STORE")) == "" {
			store.Type = storePostgres
		}
	}
	storeType, err := normalizeStoreType(store.Type)
	if err != nil {
		return runtimeStoreConfig{}, err
	}
	store.Type = storeType
	if strings.TrimSpace(store.SQLitePath) == "" {
		store.SQLitePath = defaultDBPath()
	}
	if strings.TrimSpace(store.PostgresDriver) == "" {
		store.PostgresDriver = defaultPostgresDriver
	}
	if store.Type == storePostgres && strings.TrimSpace(store.PostgresDSN) == "" {
		return store, errors.New("postgres dsn is required for Local Shared; set TUSKBASE_POSTGRES_DSN or run `tuskbase setup --mode local-shared --postgres-dsn <dsn>`")
	}
	return store, nil
}

func defaultStoreTypeForMode(mode string) string {
	switch mode {
	case modeLocalShared:
		return storePostgres
	default:
		return storeSQLite
	}
}

func normalizeStoreType(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", storeSQLite:
		return storeSQLite, nil
	case storePostgres, "pg":
		return storePostgres, nil
	default:
		return "", fmt.Errorf("unknown store %q; expected sqlite or postgres", value)
	}
}

func hasPostgresDSN(cfg userConfig) bool {
	return cfg.Store.Type == storePostgres && cfg.Store.Postgres != nil && strings.TrimSpace(cfg.Store.Postgres.DSN) != ""
}

func secretStatus(value string) string {
	if strings.TrimSpace(value) == "" {
		return "missing"
	}
	return "configured"
}

func printStoreSummary(w io.Writer, cfg userConfig) {
	switch cfg.Store.Type {
	case storePostgres:
		driver := defaultPostgresDriver
		source := ""
		if cfg.Store.Postgres != nil && strings.TrimSpace(cfg.Store.Postgres.Driver) != "" {
			driver = cfg.Store.Postgres.Driver
		}
		if cfg.Store.Postgres != nil {
			source = cfg.Store.Postgres.Source
		}
		fmt.Fprintf(w, "store: %s\n", storePostgres)
		if strings.TrimSpace(source) != "" {
			fmt.Fprintf(w, "postgres_source: %s\n", source)
		}
		fmt.Fprintf(w, "postgres_driver: %s\n", driver)
		if hasPostgresDSN(cfg) {
			fmt.Fprintf(w, "postgres_dsn: configured\n")
		} else {
			fmt.Fprintf(w, "postgres_dsn: missing (set TUSKBASE_POSTGRES_DSN or rerun setup with --postgres-dsn)\n")
		}
		if cfg.Store.Postgres != nil && cfg.Store.Postgres.Docker != nil {
			docker := cfg.Store.Postgres.Docker
			fmt.Fprintf(w, "docker_postgres_project: %s\n", docker.Project)
			fmt.Fprintf(w, "docker_postgres_image: %s\n", docker.Image)
			fmt.Fprintf(w, "docker_postgres_port: %d\n", docker.Port)
			fmt.Fprintf(w, "docker_compose: %s\n", docker.ComposePath)
		}
	default:
		fmt.Fprintf(w, "store: %s\n", storeSQLite)
		fmt.Fprintf(w, "db_path: %s\n", cfg.DBPath)
	}
}
