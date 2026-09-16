package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type commandResponse struct {
	out string
	err error
}

func prBaseRunner(responses map[string]commandResponse) commandRunner {
	return func(_ string, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		response, ok := responses[key]
		if !ok {
			return nil, fmt.Errorf("unexpected command: %s", key)
		}
		return []byte(response.out), response.err
	}
}

func ghAvailable(string) (string, error) { return "/usr/bin/gh", nil }

func TestDiscoverPRBaseRef(t *testing.T) {
	run := prBaseRunner(map[string]commandResponse{
		"git symbolic-ref --quiet --short HEAD":                                   {out: "feature\n"},
		"gh pr view --json baseRefName,baseRefOid":                                {out: `{"baseRefName":"main","baseRefOid":"abc123"}`},
		"git for-each-ref --format=%(refname:short)%00%(objectname) refs/remotes": {out: "upstream/main\x00abc123\norigin/main\x00abc123\n"},
	})

	ref, err := discoverPRBaseRef("/repo", run, ghAvailable)
	require.NoError(t, err)
	assert.Equal(t, "origin/main...HEAD", ref)
}

func TestDiscoverPRBaseRef_StackedPullRequest(t *testing.T) {
	run := prBaseRunner(map[string]commandResponse{
		"git symbolic-ref --quiet --short HEAD":                                   {out: "stack-two\n"},
		"gh pr view --json baseRefName,baseRefOid":                                {out: `{"baseRefName":"stack-one","baseRefOid":"def456"}`},
		"git for-each-ref --format=%(refname:short)%00%(objectname) refs/remotes": {out: "origin/main\x00abc123\nupstream/stack-one\x00def456\n"},
	})

	ref, err := discoverPRBaseRef("/repo", run, ghAvailable)
	require.NoError(t, err)
	assert.Equal(t, "upstream/stack-one...HEAD", ref)
}

func TestDiscoverPRBaseRef_Failures(t *testing.T) {
	tests := []struct {
		name      string
		responses map[string]commandResponse
		lookup    pathLookup
		want      string
	}{
		{
			name: "detached HEAD",
			responses: map[string]commandResponse{
				"git symbolic-ref --quiet --short HEAD": {err: errors.New("exit 1")},
			},
			lookup: ghAvailable,
			want:   "requires an attached HEAD",
		},
		{
			name: "gh unavailable",
			responses: map[string]commandResponse{
				"git symbolic-ref --quiet --short HEAD": {out: "feature\n"},
			},
			lookup: func(string) (string, error) { return "", errors.New("missing") },
			want:   "requires GitHub CLI 'gh'",
		},
		{
			name: "no pull request",
			responses: map[string]commandResponse{
				"git symbolic-ref --quiet --short HEAD":    {out: "feature\n"},
				"gh pr view --json baseRefName,baseRefOid": {out: "no pull requests found for branch feature", err: errors.New("exit 1")},
			},
			lookup: ghAvailable,
			want:   `no pull request for current branch "feature"`,
		},
		{
			name: "authentication",
			responses: map[string]commandResponse{
				"git symbolic-ref --quiet --short HEAD":    {out: "feature\n"},
				"gh pr view --json baseRefName,baseRefOid": {out: "authentication required; run gh auth login", err: errors.New("exit 1")},
			},
			lookup: ghAvailable,
			want:   "run 'gh auth login' and retry",
		},
		{
			name: "missing base metadata",
			responses: map[string]commandResponse{
				"git symbolic-ref --quiet --short HEAD":    {out: "feature\n"},
				"gh pr view --json baseRefName,baseRefOid": {out: `{"baseRefName":"","baseRefOid":""}`},
			},
			lookup: ghAvailable,
			want:   "could not discover the pull request base branch",
		},
		{
			name: "base ref unavailable",
			responses: map[string]commandResponse{
				"git symbolic-ref --quiet --short HEAD":                                   {out: "feature\n"},
				"gh pr view --json baseRefName,baseRefOid":                                {out: `{"baseRefName":"main","baseRefOid":"new123"}`},
				"git for-each-ref --format=%(refname:short)%00%(objectname) refs/remotes": {out: "origin/main\x00old123\n"},
			},
			lookup: ghAvailable,
			want:   "run 'git fetch --all' and retry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := discoverPRBaseRef("/repo", prBaseRunner(tt.responses), tt.lookup)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
