package main

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

func dockerCmd(ctx context.Context, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	return cmd.CombinedOutput()
}

func dockerRmForce(ctx context.Context, name string) error {
	_, err := dockerCmd(ctx, "rm", "-fv", name)
	return err
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func opmInstanceID() string {
	return envOr("OPM_INSTANCE", "default")
}

func requireDockerCLI() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := dockerCmd(ctx, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return fmt.Errorf("docker daemon unreachable: %w (%s)", err, truncateStr(string(out), 120))
	}
	return nil
}
