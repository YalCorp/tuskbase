package daemon_test

import (
	"testing"

	"github.com/priyavratuniyal/tuskbase/internal/daemon"
)

func TestParseLocalSharedKeys(t *testing.T) {
	keys, err := daemon.ParseLocalSharedKeys("codex:agent:codex-secret,claude:reader:claude-secret")
	if err != nil {
		t.Fatalf("ParseLocalSharedKeys() error = %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("key count = %d, want 2", len(keys))
	}
	if keys[0].Name != "codex" || keys[0].Role != "agent" || keys[0].Key != "codex-secret" {
		t.Fatalf("first key = %+v", keys[0])
	}
	if keys[1].Name != "claude" || keys[1].Role != "reader" || keys[1].Key != "claude-secret" {
		t.Fatalf("second key = %+v", keys[1])
	}
}

func TestParseLocalSharedKeysRejectsBadShape(t *testing.T) {
	_, err := daemon.ParseLocalSharedKeys("codex:secret-without-role")
	if err == nil {
		t.Fatal("ParseLocalSharedKeys() error = nil, want bad shape error")
	}
}

func TestNewLocalSharedKeyPolicyRejectsDuplicateNames(t *testing.T) {
	_, err := daemon.NewLocalSharedKeyPolicy([]daemon.LocalSharedKey{
		{Name: "codex", Role: "agent", Key: "one"},
		{Name: "codex", Role: "reader", Key: "two"},
	})
	if err == nil {
		t.Fatal("NewLocalSharedKeyPolicy() error = nil, want duplicate name error")
	}
}

func TestNewLocalSharedKeyPolicyRejectsUnknownRole(t *testing.T) {
	_, err := daemon.NewLocalSharedKeyPolicy([]daemon.LocalSharedKey{
		{Name: "codex", Role: "owner", Key: "secret"},
	})
	if err == nil {
		t.Fatal("NewLocalSharedKeyPolicy() error = nil, want unknown role error")
	}
}
