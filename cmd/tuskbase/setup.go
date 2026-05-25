package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/priyavratuniyal/tuskbase/internal/daemon"
)

const (
	modeDemo        = "demo"
	modeLocalBasic  = "local-basic"
	modeLocalShared = "local-shared"
	defaultAddr     = "127.0.0.1:8765"
)

type userConfig struct {
	Mode      string                  `json:"mode"`
	Addr      string                  `json:"addr"`
	DBPath    string                  `json:"db_path,omitempty"`
	APIKey    string                  `json:"api_key,omitempty"`
	AgentKeys []daemon.LocalSharedKey `json:"agent_keys,omitempty"`
	UpdatedAt string                  `json:"updated_at"`
}

func runSetup(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", modeLocalBasic, "demo, local-basic, or local-shared")
	clients := fs.String("client", "", "comma-separated clients to show after setup: codex, claude, cursor, or all")
	printOnly := fs.Bool("print-only", false, "print the planned setup without writing config")
	reveal := fs.Bool("reveal", false, "show generated secrets in printed output")
	yes := fs.Bool("yes", false, "accept defaults for non-interactive setup")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = yes // The current setup only writes Tuskbase-owned config, so no external confirmation is needed.

	selectedMode, err := normalizeMode(*mode)
	if err != nil {
		return err
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	cfg, found, err := loadUserConfig()
	if err != nil {
		return err
	}
	cfg.Mode = selectedMode
	if strings.TrimSpace(cfg.Addr) == "" {
		cfg.Addr = defaultAddr
	}
	if strings.TrimSpace(cfg.DBPath) == "" {
		cfg.DBPath = defaultDBPath()
	}
	cfg.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	generatedSecret := false

	switch selectedMode {
	case modeDemo:
		cfg.APIKey = ""
		cfg.AgentKeys = nil
	case modeLocalBasic:
		if strings.TrimSpace(cfg.APIKey) == "" {
			cfg.APIKey, err = generateSecret()
			if err != nil {
				return err
			}
			generatedSecret = true
		}
		cfg.AgentKeys = nil
	case modeLocalShared:
		if len(cfg.AgentKeys) == 0 {
			for _, client := range []string{"codex", "claude", "cursor"} {
				key, err := generateSecret()
				if err != nil {
					return err
				}
				cfg.AgentKeys = append(cfg.AgentKeys, daemon.LocalSharedKey{Name: client, Role: "agent", Key: key})
			}
			generatedSecret = true
		}
		cfg.APIKey = ""
	}

	fmt.Fprintf(stdout, "Tuskbase setup\n")
	fmt.Fprintf(stdout, "mode: %s\n", cfg.Mode)
	fmt.Fprintf(stdout, "addr: %s\n", cfg.Addr)
	fmt.Fprintf(stdout, "config: %s\n", path)
	if found {
		fmt.Fprintf(stdout, "existing config: reused\n")
	}
	if *printOnly {
		fmt.Fprintf(stdout, "write: skipped (--print-only)\n")
	} else if err := saveUserConfig(path, cfg); err != nil {
		return err
	} else {
		fmt.Fprintf(stdout, "write: ok\n")
	}
	if cfg.Mode != modeDemo {
		switch {
		case *printOnly && generatedSecret:
			fmt.Fprintf(stdout, "secret: generated for preview; not stored\n")
		case *printOnly:
			fmt.Fprintf(stdout, "secret: reused from existing config; not shown\n")
		case generatedSecret:
			fmt.Fprintf(stdout, "secret: generated and stored locally\n")
		default:
			fmt.Fprintf(stdout, "secret: reused from existing config\n")
		}
	}
	if *reveal {
		printSecrets(stdout, cfg)
	}
	for _, client := range parseClients(*clients) {
		fmt.Fprintln(stdout)
		printConnectConfig(stdout, client, cfg, *reveal)
	}
	return nil
}

func runConnect(args []string, stdout, stderr io.Writer) error {
	client := "generic"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		client = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "", "override setup mode for printed config: demo, local-basic, or local-shared")
	reveal := fs.Bool("reveal", false, "include stored secrets in printed commands")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		client = fs.Arg(0)
	}
	cfg, found, err := loadUserConfig()
	if err != nil {
		return err
	}
	if !found {
		cfg = userConfig{Mode: modeLocalBasic, Addr: defaultAddr}
	}
	if *mode != "" {
		cfg.Mode, err = normalizeMode(*mode)
		if err != nil {
			return err
		}
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		cfg.Addr = defaultAddr
	}
	printConnectConfig(stdout, client, cfg, *reveal)
	return nil
}

func runAuthCommand(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprintln(stdout, "Usage: tuskbase auth show --reveal")
		return nil
	}
	switch args[0] {
	case "show":
		fs := flag.NewFlagSet("auth show", flag.ContinueOnError)
		fs.SetOutput(stderr)
		reveal := fs.Bool("reveal", false, "reveal stored local secrets")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		cfg, found, err := loadUserConfig()
		if err != nil {
			return err
		}
		if !found {
			return errors.New("no Tuskbase setup found; run `tuskbase setup` first")
		}
		fmt.Fprintf(stdout, "mode: %s\n", cfg.Mode)
		fmt.Fprintf(stdout, "auth_policy: %s\n", authPolicyName(cfg))
		if *reveal {
			printSecrets(stdout, cfg)
		} else if cfg.Mode != modeDemo {
			fmt.Fprintln(stdout, "secret: hidden; rerun with --reveal to show it")
		}
		return nil
	default:
		return fmt.Errorf("unknown auth command %q", args[0])
	}
}

func normalizeMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", modeLocalBasic, "basic", "local":
		return modeLocalBasic, nil
	case modeDemo:
		return modeDemo, nil
	case modeLocalShared, "shared":
		return modeLocalShared, nil
	default:
		return "", fmt.Errorf("unknown setup mode %q", mode)
	}
}

func parseClients(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.EqualFold(raw, "all") {
		return []string{"codex", "claude", "cursor"}
	}
	parts := strings.Split(raw, ",")
	clients := make([]string, 0, len(parts))
	for _, part := range parts {
		client := strings.ToLower(strings.TrimSpace(part))
		if client != "" {
			clients = append(clients, client)
		}
	}
	return clients
}

func printConnectConfig(w io.Writer, client string, cfg userConfig, reveal bool) {
	client = strings.ToLower(strings.TrimSpace(client))
	if client == "" {
		client = "generic"
	}
	url := "http://" + cfg.Addr + "/mcp"
	token := tokenForClient(cfg, client, reveal)
	switch client {
	case "codex":
		fmt.Fprintf(w, "# Codex MCP config\n")
		if cfg.Mode == modeDemo {
			fmt.Fprintf(w, "codex mcp add tuskbase -- tuskbase serve\n")
			return
		}
		fmt.Fprintf(w, "codex mcp add tuskbase --url %s --bearer-token-env-var TUSKBASE_API_KEY\n", url)
		fmt.Fprintf(w, "# Codex reads the bearer token from TUSKBASE_API_KEY.\n")
		if reveal && token != "" {
			fmt.Fprintf(w, "export TUSKBASE_API_KEY=%s\n", token)
		} else {
			fmt.Fprintf(w, "# Run `tuskbase auth show --reveal` if you need to copy the token.\n")
		}
	case "claude", "claude-code":
		fmt.Fprintf(w, "# Claude Code MCP config\n")
		if cfg.Mode == modeDemo {
			fmt.Fprintf(w, "claude mcp add --transport stdio tuskbase -- tuskbase serve\n")
			return
		}
		fmt.Fprintf(w, "claude mcp add --transport http tuskbase %s --header \"Authorization: Bearer %s\"\n", url, token)
	case "cursor":
		fmt.Fprintf(w, "# Cursor MCP config (~/.cursor/mcp.json)\n")
		if cfg.Mode == modeDemo {
			fmt.Fprintf(w, "{\n  \"mcpServers\": {\n    \"tuskbase\": {\n      \"command\": \"tuskbase\",\n      \"args\": [\"serve\"]\n    }\n  }\n}\n")
			return
		}
		fmt.Fprintf(w, "{\n  \"mcpServers\": {\n    \"tuskbase\": {\n      \"type\": \"streamable-http\",\n      \"url\": %q,\n      \"headers\": {\n        \"Authorization\": \"Bearer %s\"\n      }\n    }\n  }\n}\n", url, token)
	default:
		fmt.Fprintf(w, "# Generic HTTP MCP config\n")
		if cfg.Mode == modeDemo {
			fmt.Fprintf(w, "command: tuskbase\nargs: [\"serve\"]\n")
			return
		}
		fmt.Fprintf(w, "url: %s\nAuthorization: Bearer %s\n", url, token)
	}
}

func tokenForClient(cfg userConfig, client string, reveal bool) string {
	if cfg.Mode == modeLocalShared {
		for _, key := range cfg.AgentKeys {
			if strings.EqualFold(key.Name, client) {
				if reveal {
					return key.Key
				}
				return "<" + key.Name + "-key>"
			}
		}
	}
	if reveal && cfg.APIKey != "" {
		return cfg.APIKey
	}
	return "<tuskbase-key>"
}

func printSecrets(w io.Writer, cfg userConfig) {
	if cfg.APIKey != "" {
		fmt.Fprintf(w, "TUSKBASE_API_KEY=%s\n", cfg.APIKey)
	}
	if len(cfg.AgentKeys) > 0 {
		parts := make([]string, 0, len(cfg.AgentKeys))
		for _, key := range cfg.AgentKeys {
			parts = append(parts, fmt.Sprintf("%s:%s:%s", key.Name, key.Role, key.Key))
		}
		sort.Strings(parts)
		fmt.Fprintf(w, "TUSKBASE_AGENT_KEYS=%s\n", strings.Join(parts, ","))
	}
}

func authPolicyName(cfg userConfig) string {
	if len(cfg.AgentKeys) > 0 {
		return "local-shared-keys"
	}
	if cfg.APIKey != "" {
		return "local-api-key"
	}
	return "none"
}

func generateSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "tbk_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

func configPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("TUSKBASE_CONFIG_PATH")); path != "" {
		return path, nil
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "tuskbase", "config.json"), nil
}

func loadUserConfig() (userConfig, bool, error) {
	path, err := configPath()
	if err != nil {
		return userConfig{}, false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return userConfig{}, false, nil
	}
	if err != nil {
		return userConfig{}, false, err
	}
	var cfg userConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return userConfig{}, false, err
	}
	return cfg, true, nil
}

func saveUserConfig(path string, cfg userConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}
