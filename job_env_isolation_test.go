package main

import (
	"strings"
	"testing"

	openjobenv "github.com/TheGrimmChester/open-job-env-go"
)

func TestHostToolEnvOmitsRedisURL(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://:secret@redis-opm:6379/0")
	t.Setenv("OAM_SECRET_KEY", "should-not-leak")
	t.Setenv("JWT_SECRET", "jwt-should-not-leak")
	t.Setenv("PATH", "/usr/bin")
	env := openjobenv.HostToolEnv("GIT_ASKPASS=/tmp/ask.sh")
	joined := strings.Join(env, "\n")
	for _, deny := range []string{"REDIS_URL=", "OAM_SECRET_KEY=", "JWT_SECRET="} {
		if strings.Contains(joined, deny) {
			t.Fatalf("HostToolEnv leaked %s in %#v", deny, env)
		}
	}
	found := false
	for _, line := range env {
		if line == "GIT_ASKPASS=/tmp/ask.sh" {
			found = true
		}
	}
	if !found {
		t.Fatalf("askpass missing: %#v", env)
	}
}
