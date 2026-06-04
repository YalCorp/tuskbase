package main

import (
	"strings"
	"testing"
)

func TestDockerComposeYAMLRendersSinglePostgresVolumeTarget(t *testing.T) {
	cfg := dockerPostgresConfig{
		Service:  "postgres",
		Image:    "pgvector/pgvector:pg16",
		Host:     "127.0.0.1",
		Port:     8766,
		Database: "tuskbase",
		User:     "tuskbase",
		Volume:   "tuskbase-local-shared-postgres",
	}
	compose := dockerComposeYAML(cfg, "secret")
	want := "- tuskbase-local-shared-postgres:/var/lib/postgresql/data"
	if !strings.Contains(compose, want) {
		t.Fatalf("compose volume missing %q:\n%s", want, compose)
	}
	if strings.Contains(compose, ":/var/lib/postgresql/data:/var/lib/postgresql/data") {
		t.Fatalf("compose volume target rendered twice:\n%s", compose)
	}
}
