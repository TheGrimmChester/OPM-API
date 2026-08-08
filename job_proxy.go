package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	openegress "github.com/TheGrimmChester/open-egress-proxy/orchestrate"
)

const (
	egressProxyRoleLabel = "egress-proxy"
	egressProxyPort      = 3128
	egressProxyAlias     = "open-egress-proxy"
)

var defaultAIAllowHosts = []string{
	"api.cursor.sh",
	"api2.cursor.sh",
	"api3.cursor.sh",
	"api4.cursor.sh",
	"api5.cursor.sh",
}

func egressProxyEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPM_JOB_EGRESS_PROXY"))) {
	case "0", "off", "false", "no":
		return false
	default:
		return true
	}
}

func aiEgressAllowlist() []string {
	if v := strings.TrimSpace(os.Getenv("OPA_JOB_EGRESS_ALLOWLIST")); v != "" {
		return parseEgressAllowlist(v)
	}
	return append([]string{}, defaultAIAllowHosts...)
}

func parseEgressAllowlist(raw string) []string {
	return openegress.ParseAllowlist(raw)
}

func egressProxyImage() string {
	if v := strings.TrimSpace(os.Getenv("OPA_JOB_EGRESS_PROXY_IMAGE")); v != "" {
		return v
	}
	tag := nz(strings.TrimSpace(os.Getenv("OPM_RUNNER_TAG")), "nas")
	if tag == "smoke" {
		tag = "nas"
	}
	return "open-egress-proxy:" + tag
}

func egressProxyContainerName() string {
	return "opm-egress-proxy-" + opmInstanceID()
}

func egressNetworkName() string {
	return "opm-egress-" + opmInstanceID()
}

func egressStackNetworks() []string {
	if v, ok := os.LookupEnv("OPA_JOB_EGRESS_STACK_NETWORKS"); ok {
		return parseEgressAllowlist(v)
	}
	return []string{"opa-stack_opa_internal", "opa_network"}
}

type opmEgressDocker struct{}

func (opmEgressDocker) Cmd(ctx context.Context, args ...string) ([]byte, error) {
	return dockerCmd(ctx, args...)
}

func (opmEgressDocker) RmForce(ctx context.Context, name string) error {
	return dockerRmForce(ctx, name)
}

func egressOrchestrateConfig() openegress.Config {
	return openegress.Config{
		ContainerName: egressProxyContainerName(),
		NetworkName:   egressNetworkName(),
		Image:         egressProxyImage(),
		Allowlist:     aiEgressAllowlist(),
		OwnerLabel:    "opm.owner=opm-api",
		InstanceLabel: "opm.instance=" + opmInstanceID(),
		RoleLabelKey:  "opm.role",
		StackNetworks: egressStackNetworks(),
		Alias:         egressProxyAlias,
		Port:          egressProxyPort,
	}
}

func egressProxyEnvVars() map[string]string {
	return openegress.ProxyEnvVars(egressProxyAlias, egressProxyPort)
}

func ensureSharedEgressProxy(ctx context.Context) (string, error) {
	if !egressProxyEnabled() {
		return "", fmt.Errorf("egress proxy disabled")
	}
	if err := requireDockerCLI(); err != nil {
		return "", err
	}
	return openegress.EnsureShared(ctx, opmEgressDocker{}, egressOrchestrateConfig())
}

func attachEgressProxyToJobNetwork(ctx context.Context, jobNet string) error {
	if err := requireDockerCLI(); err != nil {
		return err
	}
	return openegress.AttachToNetwork(ctx, opmEgressDocker{}, egressOrchestrateConfig(), jobNet)
}

func detachEgressProxyFromNetwork(ctx context.Context, jobNet string) {
	openegress.DetachFromNetwork(ctx, opmEgressDocker{}, egressOrchestrateConfig(), jobNet)
}

func prepareAIJobNetwork(ctx context.Context, jobID string) (string, error) {
	netName, err := createJobInternalNetwork(ctx, jobID)
	if err != nil {
		return "", err
	}
	if !egressProxyEnabled() {
		return netName, nil
	}
	if err := attachEgressProxyToJobNetwork(ctx, netName); err != nil {
		_ = removeJobInternalNetwork(ctx, jobID)
		return "", err
	}
	return netName, nil
}
