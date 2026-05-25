package daemon

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	localAPIKeyScope = "tuskbase"
	roleReader       = "reader"
	roleAgent        = "agent"
	roleAdmin        = "admin"
)

// LocalAPIKeyPolicy protects HTTP transports with a single local bearer token.
// It is intentionally small: Local Shared should use named keys instead.
type LocalAPIKeyPolicy struct {
	key string
}

func NewLocalAPIKeyPolicy(key string) (LocalAPIKeyPolicy, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return LocalAPIKeyPolicy{}, errors.New("local API key is required")
	}
	return LocalAPIKeyPolicy{key: key}, nil
}

func (p LocalAPIKeyPolicy) WrapHTTP(h http.Handler) http.Handler {
	verifier := func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		if !sameSecret(token, p.key) {
			return nil, auth.ErrInvalidToken
		}
		return tokenInfo("local-api-key", roleAgent), nil
	}
	return requireTuskbaseBearer(verifier)(h)
}

func (LocalAPIKeyPolicy) Name() string { return "local-api-key" }

// LocalSharedKey describes one named local client credential.
type LocalSharedKey struct {
	Name string
	Role string
	Key  string
}

// LocalSharedKeyPolicy protects HTTP transports with per-agent local bearer tokens.
// This is the Local Shared direction without introducing persistent key management yet.
type LocalSharedKeyPolicy struct {
	keys []LocalSharedKey
}

func NewLocalSharedKeyPolicy(keys []LocalSharedKey) (LocalSharedKeyPolicy, error) {
	if len(keys) == 0 {
		return LocalSharedKeyPolicy{}, errors.New("at least one local shared key is required")
	}
	seen := map[string]struct{}{}
	cleaned := make([]LocalSharedKey, 0, len(keys))
	for _, key := range keys {
		key.Name = strings.TrimSpace(key.Name)
		role, err := parseRole(key.Role)
		if err != nil {
			return LocalSharedKeyPolicy{}, err
		}
		key.Role = role
		key.Key = strings.TrimSpace(key.Key)
		if key.Name == "" {
			return LocalSharedKeyPolicy{}, errors.New("local shared key name is required")
		}
		if key.Key == "" {
			return LocalSharedKeyPolicy{}, fmt.Errorf("local shared key for %q is required", key.Name)
		}
		if _, ok := seen[key.Name]; ok {
			return LocalSharedKeyPolicy{}, fmt.Errorf("duplicate local shared key name %q", key.Name)
		}
		seen[key.Name] = struct{}{}
		cleaned = append(cleaned, key)
	}
	return LocalSharedKeyPolicy{keys: cleaned}, nil
}

// ParseLocalSharedKeys parses comma-separated name:role:key specs.
// Example: codex:agent:secret,claude:reader:other-secret
func ParseLocalSharedKeys(raw string) ([]LocalSharedKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	keys := make([]LocalSharedKey, 0, len(parts))
	for _, part := range parts {
		fields := strings.SplitN(strings.TrimSpace(part), ":", 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("local shared key %q must use name:role:key", part)
		}
		keys = append(keys, LocalSharedKey{Name: fields[0], Role: fields[1], Key: fields[2]})
	}
	return keys, nil
}

func (p LocalSharedKeyPolicy) WrapHTTP(h http.Handler) http.Handler {
	verifier := func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		for _, key := range p.keys {
			if sameSecret(token, key.Key) {
				return tokenInfo(key.Name, key.Role), nil
			}
		}
		return nil, auth.ErrInvalidToken
	}
	return requireTuskbaseBearer(verifier)(h)
}

func (LocalSharedKeyPolicy) Name() string { return "local-shared-keys" }

func requireTuskbaseBearer(verifier auth.TokenVerifier) func(http.Handler) http.Handler {
	return auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{Scopes: []string{localAPIKeyScope}})
}

func tokenInfo(userID, role string) *auth.TokenInfo {
	return &auth.TokenInfo{
		Scopes:     roleScopes(role),
		Expiration: time.Now().Add(time.Hour),
		UserID:     userID,
		Extra:      map[string]any{"role": role},
	}
}

func roleScopes(role string) []string {
	switch role {
	case roleReader:
		return []string{localAPIKeyScope, "tuskbase:read"}
	case roleAgent:
		return []string{localAPIKeyScope, "tuskbase:read", "tuskbase:write"}
	case roleAdmin:
		return []string{localAPIKeyScope, "tuskbase:read", "tuskbase:write", "tuskbase:admin"}
	default:
		return []string{localAPIKeyScope}
	}
}

func parseRole(role string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case roleReader:
		return roleReader, nil
	case "", roleAgent:
		return roleAgent, nil
	case roleAdmin:
		return roleAdmin, nil
	default:
		return "", fmt.Errorf("unsupported local shared role %q", role)
	}
}

func sameSecret(got, want string) bool {
	gotHash := sha256.Sum256([]byte(got))
	wantHash := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(gotHash[:], wantHash[:]) == 1
}
