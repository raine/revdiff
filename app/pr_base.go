package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type commandRunner func(dir, name string, args ...string) ([]byte, error)

type pathLookup func(string) (string, error)

type pullRequestBase struct {
	Name string `json:"baseRefName"`
	OID  string `json:"baseRefOid"`
}

func resolvePRBaseRef(repoRoot string) (string, error) {
	return discoverPRBaseRef(repoRoot, runCommand, exec.LookPath)
}

func runCommand(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(context.Background(), name, args...) //nolint:gosec // fixed tools with internally constructed arguments
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

func discoverPRBaseRef(repoRoot string, run commandRunner, lookup pathLookup) (string, error) {
	branchOut, err := run(repoRoot, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", errors.New("--base requires an attached HEAD; check out the pull request branch and retry")
	}
	branch := strings.TrimSpace(string(branchOut))
	if branch == "" {
		return "", errors.New("--base could not determine the current branch")
	}

	if _, err := lookup("gh"); err != nil {
		return "", errors.New("--base requires GitHub CLI 'gh'; install it, then run 'gh auth login'")
	}
	prOut, err := run(repoRoot, "gh", "pr", "view", "--json", "baseRefName,baseRefOid")
	if err != nil {
		return "", prBaseDiscoveryError(branch, prOut)
	}

	var base pullRequestBase
	if err := json.Unmarshal(prOut, &base); err != nil {
		return "", fmt.Errorf("--base could not parse pull request metadata from gh: %w", err)
	}
	base.Name = strings.TrimSpace(base.Name)
	base.OID = strings.TrimSpace(base.OID)
	if base.Name == "" || base.OID == "" {
		return "", errors.New("--base could not discover the pull request base branch from gh")
	}

	refsOut, err := run(repoRoot, "git", "for-each-ref", "--format=%(refname:short)%00%(objectname)", "refs/remotes")
	if err != nil {
		return "", fmt.Errorf("--base could not inspect remote branches: %s", commandErrorDetail(err, refsOut))
	}
	if remoteRef := matchingRemoteRef(string(refsOut), base); remoteRef != "" {
		return remoteRef + "...HEAD", nil
	}

	return "", fmt.Errorf("--base pull request base %q is unavailable locally; run 'git fetch --all' and retry", base.Name)
}

func matchingRemoteRef(output string, base pullRequestBase) string {
	var fallback string
	for line := range strings.SplitSeq(output, "\n") {
		ref, oid, ok := strings.Cut(line, "\x00")
		if !ok || oid != base.OID || !strings.HasSuffix(ref, "/"+base.Name) {
			continue
		}
		if ref == "origin/"+base.Name {
			return ref
		}
		if fallback == "" {
			fallback = ref
		}
	}
	return fallback
}

func prBaseDiscoveryError(branch string, output []byte) error {
	detail := strings.TrimSpace(string(output))
	lower := strings.ToLower(detail)
	switch {
	case strings.Contains(lower, "no pull requests found"),
		strings.Contains(lower, "could not resolve to a pull request"):
		return fmt.Errorf("--base found no pull request for current branch %q", branch)
	case strings.Contains(lower, "authentication"), strings.Contains(lower, "auth login"),
		strings.Contains(lower, "http 401"), strings.Contains(lower, "bad credentials"):
		return fmt.Errorf("--base could not authenticate with GitHub; run 'gh auth login' and retry: %s", detail)
	default:
		return fmt.Errorf("--base could not discover the pull request base with gh: %s", commandErrorDetail(errors.New("gh failed"), output))
	}
}

func commandErrorDetail(err error, output []byte) string {
	if detail := strings.TrimSpace(string(output)); detail != "" {
		return detail
	}
	return err.Error()
}
