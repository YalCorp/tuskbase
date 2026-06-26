package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/priyavratuniyal/tuskbase/internal/app"
	"github.com/priyavratuniyal/tuskbase/internal/domain"
)

func runImportCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprintln(stdout, "Usage: tuskbase import <scan|list|show|accept|reject>")
		return nil
	}
	switch args[0] {
	case "scan":
		return runImportScan(ctx, args[1:], stdout, stderr)
	case "list":
		return runImportList(ctx, args[1:], stdout, stderr)
	case "show":
		return runImportShow(ctx, args[1:], stdout, stderr)
	case "accept":
		return runImportAccept(ctx, args[1:], stdout, stderr)
	case "reject":
		return runImportReject(ctx, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown import command %q", args[0])
	}
}

func runImportScan(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("import scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", "", "repo path to scan")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*repo) == "" {
		return errors.New("import scan requires --repo")
	}
	var out app.ImportScanOutput
	if err := doControlJSON(ctx, http.MethodPost, "/control/v1/import/scan", app.ImportScanInput{RepoPath: *repo}, &out); err != nil {
		return err
	}
	p := newPresenter(stdout)
	p.KV("workspace", out.Workspace.ID)
	p.KV("scanned_docs", fmt.Sprintf("%d", out.Scanned))
	p.KV("candidates", fmt.Sprintf("%d", len(out.Candidates)))
	p.KV("pending", fmt.Sprintf("%d", out.ByStatus[string(domain.CandidatePending)]))
	p.KV("accepted", fmt.Sprintf("%d", out.ByStatus[string(domain.CandidateAccepted)]))
	p.KV("rejected", fmt.Sprintf("%d", out.ByStatus[string(domain.CandidateRejected)]))
	return nil
}

func runImportList(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("import list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspaceID := fs.String("workspace", "", "workspace id")
	status := fs.String("status", "pending", "pending, accepted, rejected, or all")
	limit := fs.Int("limit", 50, "maximum candidates to list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*workspaceID) == "" {
		return errors.New("import list requires --workspace")
	}
	q := url.Values{}
	q.Set("workspace_id", *workspaceID)
	q.Set("status", *status)
	if *limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", *limit))
	}
	var out app.ImportListOutput
	if err := doControlJSON(ctx, http.MethodGet, "/control/v1/import/candidates?"+q.Encode(), nil, &out); err != nil {
		return err
	}
	printCandidateList(stdout, out.Candidates)
	return nil
}

func runImportShow(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("import show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: tuskbase import show <candidate-id>")
	}
	var out app.ImportShowOutput
	if err := doControlJSON(ctx, http.MethodGet, "/control/v1/import/candidates/"+url.PathEscape(fs.Arg(0)), nil, &out); err != nil {
		return err
	}
	printCandidate(stdout, out.Candidate)
	return nil
}

func runImportAccept(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("import accept", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: tuskbase import accept <candidate-id>")
	}
	var out app.ImportAcceptOutput
	if err := doControlJSON(ctx, http.MethodPost, "/control/v1/import/candidates/"+url.PathEscape(fs.Arg(0))+"/accept", nil, &out); err != nil {
		return err
	}
	p := newPresenter(stdout)
	p.KV("candidate", out.Candidate.ID)
	p.KV("status", string(out.Candidate.Status))
	p.KV("decision", out.Decision.ID)
	p.KV("indexing_status", out.IndexingStatus)
	if out.IndexingError != "" {
		p.KV("indexing_error", out.IndexingError)
	}
	return nil
}

func runImportReject(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("import reject", flag.ContinueOnError)
	fs.SetOutput(stderr)
	summary := fs.String("summary", "", "optional rejection summary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: tuskbase import reject <candidate-id> [--summary <text>]")
	}
	var out app.ImportRejectOutput
	if err := doControlJSON(ctx, http.MethodPost, "/control/v1/import/candidates/"+url.PathEscape(fs.Arg(0))+"/reject", app.ImportRejectInput{Summary: *summary}, &out); err != nil {
		return err
	}
	p := newPresenter(stdout)
	p.KV("candidate", out.Candidate.ID)
	p.KV("status", string(out.Candidate.Status))
	if out.Candidate.RejectionSummary != "" {
		p.KV("summary", out.Candidate.RejectionSummary)
	}
	return nil
}

func doControlJSON(ctx context.Context, method, path string, in, out any) error {
	cfg, credential, err := controlClientConfig(ctx)
	if err != nil {
		return err
	}
	if err := newLifecycleController().EnsureReady(ctx, cfg); err != nil {
		return fmt.Errorf("wake Tuskbase daemon: %w", err)
	}
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+cfg.Addr+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+credential.Token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("control API status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func controlClientConfig(ctx context.Context) (userConfig, ClientCredential, error) {
	cfg, found, err := loadUserConfig()
	if err != nil {
		return userConfig{}, ClientCredential{}, err
	}
	if !found {
		return userConfig{}, ClientCredential{}, errors.New("no Tuskbase setup found; run `tuskbase setup` first")
	}
	cfg = normalizedDaemonConfig(cfg)
	credential, err := controlCredential(ctx, cfg)
	if err != nil {
		return userConfig{}, ClientCredential{}, err
	}
	return cfg, credential, nil
}

func controlCredential(ctx context.Context, cfg userConfig) (ClientCredential, error) {
	profile, err := AuthProfileForConfig(cfg)
	if err != nil {
		return ClientCredential{}, err
	}
	if cfg.Mode == modeLocalShared {
		client := preferredLocalSharedClient(cfg)
		return profile.Credential(ctx, client)
	}
	return profile.Credential(ctx, "generic")
}

func preferredLocalSharedClient(cfg userConfig) string {
	for _, preferred := range []string{"tuskbase", "codex", "generic"} {
		for _, key := range cfg.AgentKeys {
			if strings.EqualFold(key.Name, preferred) {
				return key.Name
			}
		}
	}
	if len(cfg.AgentKeys) > 0 {
		return cfg.AgentKeys[0].Name
	}
	return "generic"
}

func printCandidateList(w io.Writer, candidates []domain.DecisionCandidate) {
	p := newPresenter(w)
	if len(candidates) == 0 {
		p.KV("candidates", "0")
		return
	}
	for _, candidate := range candidates {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", candidate.ID, candidate.Status, candidate.SourcePath, candidate.Title)
	}
}

func printCandidate(w io.Writer, candidate domain.DecisionCandidate) {
	p := newPresenter(w)
	p.KV("id", candidate.ID)
	p.KV("workspace", candidate.WorkspaceID)
	p.KV("status", string(candidate.Status))
	p.KV("type", candidate.Type)
	p.KV("title", candidate.Title)
	p.KV("outcome", candidate.Outcome)
	if candidate.Rationale != "" {
		p.KV("rationale", candidate.Rationale)
	}
	p.KV("confidence", fmt.Sprintf("%.2f", candidate.Confidence))
	p.KV("source_path", candidate.SourcePath)
	if candidate.SourceTitle != "" {
		p.KV("source_title", candidate.SourceTitle)
	}
	p.KV("source_snippet", candidate.SourceSnippet)
	p.KV("detector", candidate.Detector)
	if candidate.AcceptedDecisionID != "" {
		p.KV("accepted_decision_id", candidate.AcceptedDecisionID)
	}
	if candidate.RejectionSummary != "" {
		p.KV("rejection_summary", candidate.RejectionSummary)
	}
}
