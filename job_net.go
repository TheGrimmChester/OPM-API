package main

import (
	"context"
	"fmt"
	"strings"
)

const networkModeInternalProxy = "internal+proxy"

func jobNetworkName(jobID string) string {
	return "opm-job-" + sanitizeDockerName(jobID)
}

func createJobInternalNetwork(ctx context.Context, jobID string) (string, error) {
	name := jobNetworkName(jobID)
	if err := requireDockerCLI(); err != nil {
		return "", err
	}
	out, err := dockerCmd(ctx, "network", "create", "--internal", name)
	if err != nil && !strings.Contains(strings.ToLower(string(out)+err.Error()), "already") {
		return "", fmt.Errorf("network create: %w (%s)", err, truncateStr(string(out), 160))
	}
	return name, nil
}

func removeJobInternalNetwork(ctx context.Context, jobID string) error {
	if err := requireDockerCLI(); err != nil {
		return nil
	}
	name := jobNetworkName(jobID)
	detachEgressProxyFromNetwork(ctx, name)
	_, _ = dockerCmd(ctx, "network", "rm", name)
	return nil
}
