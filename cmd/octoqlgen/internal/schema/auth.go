// Copyright (c) 2026 octoql contributors
// SPDX-License-Identifier: MIT

package schema

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type commandRunner interface {
	Run(context.Context, string, ...string) ([]byte, []byte, error)
}

type execRunner struct{}

func (execRunner) Run(
	ctx context.Context,
	name string,
	arguments ...string,
) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	stdout, err := command.Output()
	if err == nil {
		return stdout, nil, nil
	}

	exitError, ok := errors.AsType[*exec.ExitError](err)
	if ok {
		return stdout, exitError.Stderr, err
	}
	return stdout, nil, err
}

func discoverToken(
	ctx context.Context,
	host string,
	lookupEnvironment func(string) (string, bool),
	runner commandRunner,
) (string, error) {
	token, found := lookupEnvironment("GH_TOKEN")
	if found && token != "" {
		return token, nil
	}
	token, found = lookupEnvironment("GITHUB_TOKEN")
	if found && token != "" {
		return token, nil
	}

	stdout, stderr, err := runner.Run(
		ctx,
		"gh",
		"auth",
		"token",
		"--hostname",
		host,
	)
	if err == nil {
		return strings.TrimSpace(string(stdout)), nil
	}
	if errors.Is(err, exec.ErrNotFound) || isNotAuthenticated(stderr) {
		return "", nil
	}
	return "", fmt.Errorf("discovering github token with gh: %w", err)
}

func isNotAuthenticated(stderr []byte) bool {
	message := strings.ToLower(string(stderr))
	return strings.Contains(message, "not logged into") ||
		strings.Contains(message, "no oauth token") ||
		strings.Contains(message, "not authenticated") ||
		strings.Contains(message, "authentication token is missing")
}
