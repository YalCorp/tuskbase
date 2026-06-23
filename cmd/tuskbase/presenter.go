package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiCyan  = "\x1b[36m"
	ansiGreen = "\x1b[32m"
	ansiAmber = "\x1b[33m"
	ansiRed   = "\x1b[31m"
)

type presenter struct {
	w      io.Writer
	pretty bool
}

func newPresenter(w io.Writer) presenter {
	return presenter{w: w, pretty: shouldPretty(w)}
}

func shouldPretty(w io.Writer) bool {
	if truthyEnv("TUSKBASE_PLAIN") {
		return false
	}
	if truthyEnv("TUSKBASE_PRETTY") {
		return true
	}
	if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" || strings.EqualFold(os.Getenv("TERM"), "dumb") || truthyEnv("CI") {
		return false
	}
	file, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	if stdoutFile, ok := w.(*os.File); ok {
		info, err = stdoutFile.Stat()
		if err != nil {
			return false
		}
	} else if file.Fd() != os.Stdout.Fd() {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func truthyEnv(key string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch value {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (p presenter) Header() {
	if !p.pretty {
		return
	}
	fmt.Fprintf(p.w, "%stuskbase%s\n", ansiBold+ansiCyan, ansiReset)
	fmt.Fprintf(p.w, "%slocal-first repo memory for coding agents%s\n\n", ansiDim, ansiReset)
}

func (p presenter) Section(name string) {
	if !p.pretty {
		return
	}
	fmt.Fprintf(p.w, "\n%s%s%s\n", ansiBold, name, ansiReset)
}

func (p presenter) Hint(format string, args ...any) {
	if !p.pretty {
		return
	}
	fmt.Fprintf(p.w, "%s", ansiDim)
	fmt.Fprintf(p.w, format, args...)
	fmt.Fprintf(p.w, "%s", ansiReset)
	if !strings.HasSuffix(format, "\n") {
		fmt.Fprintln(p.w)
	}
}

func (p presenter) KV(key, value string) {
	if p.pretty {
		fmt.Fprintf(p.w, "  %s: %s\n", key, p.Status(value))
		return
	}
	fmt.Fprintf(p.w, "%s: %s\n", key, value)
}

func (p presenter) Line(format string, args ...any) {
	fmt.Fprintf(p.w, format, args...)
	if !strings.HasSuffix(format, "\n") {
		fmt.Fprintln(p.w)
	}
}

func (p presenter) Status(value string) string {
	if !p.pretty {
		return value
	}
	label := strings.TrimSpace(value)
	color := ""
	switch label {
	case "ready", "running", "ok", "configured", "enabled":
		color = ansiGreen
	case "skipped", "missing", "disabled", "not-ready", "down", "unavailable", "unknown":
		color = ansiAmber
	case "degraded", "auth-failed", "connect-failed", "failed":
		color = ansiRed
	}
	if color == "" {
		return value
	}
	return color + value + ansiReset
}

func statusLabel(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "ok", "healthy", "running":
		return "ready"
	case "not-ready":
		return "down"
	case "not configured", "missing":
		return "missing"
	default:
		return value
	}
}
