package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	egressProxyRoleLabel = "egress-proxy"
	egressProxyPort      = 3128
	egressProxyAlias     = "open-egress-proxy"
)

var egressProxyMu sync.Mutex

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
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == ';'
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		h := strings.ToLower(strings.TrimSpace(f))
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
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

func attachEgressProxyToStackNetworks(ctx context.Context, proxyName string) {
	for _, netName := range egressStackNetworks() {
		netName = strings.TrimSpace(netName)
		if netName == "" {
			continue
		}
		if _, err := dockerCmd(ctx, "network", "inspect", netName); err != nil {
			continue
		}
		out, err := dockerCmd(ctx, "network", "connect", netName, proxyName)
		if err != nil {
			low := strings.ToLower(string(out) + err.Error())
			if strings.Contains(low, "already") {
				continue
			}
		}
	}
}

func egressProxyEnvVars() map[string]string {
	url := fmt.Sprintf("http://%s:%d", egressProxyAlias, egressProxyPort)
	return map[string]string{
		"HTTP_PROXY":  url,
		"HTTPS_PROXY": url,
		"http_proxy":  url,
		"https_proxy": url,
		"NO_PROXY":    "localhost,127.0.0.1," + egressProxyAlias,
		"no_proxy":    "localhost,127.0.0.1," + egressProxyAlias,
	}
}

func ensureSharedEgressProxy(ctx context.Context) (string, error) {
	if !egressProxyEnabled() {
		return "", fmt.Errorf("egress proxy disabled")
	}
	if err := requireDockerCLI(); err != nil {
		return "", err
	}
	egressProxyMu.Lock()
	defer egressProxyMu.Unlock()

	name := egressProxyContainerName()
	wantAllow := strings.Join(aiEgressAllowlist(), ",")
	if out, err := dockerCmd(ctx, "inspect", "-f", "{{.State.Running}}", name); err == nil && strings.TrimSpace(string(out)) == "true" {
		attachEgressProxyToStackNetworks(ctx, name)
		return name, nil
	}
	_ = dockerRmForce(ctx, name)

	egNet := egressNetworkName()
	if out, err := dockerCmd(ctx, "network", "create", egNet); err != nil {
		low := strings.ToLower(string(out) + err.Error())
		if !strings.Contains(low, "already") {
			return "", fmt.Errorf("egress network: %w (%s)", err, truncateStr(string(out), 160))
		}
	}

	image := egressProxyImage()
	argv := []string{
		"run", "-d",
		"--name", name,
		"--label", "opm.owner=opm-api",
		"--label", "opm.role=" + egressProxyRoleLabel,
		"--label", "opm.instance=" + opmInstanceID(),
		"--network", egNet,
		"--restart", "unless-stopped",
		"-e", "OPA_EGRESS_ALLOWLIST=" + wantAllow,
		"-e", "OPA_EGRESS_PROXY_LISTEN=:" + strconv.Itoa(egressProxyPort),
		image,
	}
	out, err := dockerCmd(ctx, argv...)
	if err != nil {
		return "", fmt.Errorf("egress proxy start: %w (%s)", err, truncateStr(string(out), 240))
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if running, _ := dockerCmd(ctx, "inspect", "-f", "{{.State.Running}}", name); strings.TrimSpace(string(running)) == "true" {
			attachEgressProxyToStackNetworks(ctx, name)
			return name, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	attachEgressProxyToStackNetworks(ctx, name)
	return name, nil
}

func attachEgressProxyToJobNetwork(ctx context.Context, jobNet string) error {
	jobNet = strings.TrimSpace(jobNet)
	if jobNet == "" {
		return fmt.Errorf("empty job network")
	}
	proxy, err := ensureSharedEgressProxy(ctx)
	if err != nil {
		return err
	}
	out, err := dockerCmd(ctx, "network", "connect", "--alias", egressProxyAlias, jobNet, proxy)
	if err != nil {
		low := strings.ToLower(string(out) + err.Error())
		if strings.Contains(low, "already") {
			return nil
		}
		return fmt.Errorf("proxy network connect: %w (%s)", err, truncateStr(string(out), 160))
	}
	return nil
}

func detachEgressProxyFromNetwork(ctx context.Context, jobNet string) {
	jobNet = strings.TrimSpace(jobNet)
	if jobNet == "" {
		return
	}
	name := egressProxyContainerName()
	_, _ = dockerCmd(ctx, "network", "disconnect", "-f", jobNet, name)
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
