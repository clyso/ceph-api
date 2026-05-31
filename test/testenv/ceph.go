// Package testenv brings up a real Ceph cluster in Docker for e2e tests.
package testenv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	mobycontainer "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
)

const (
	networkSubnet  = "192.168.56.0/24"
	networkGateway = "192.168.56.1"

	MonIP             = "192.168.56.7"
	cephPublicNetwork = "192.168.56.0/24"

	DashboardPort = 8443
	RestfulPort   = 8003
	RGWPort       = 8080

	DashboardUser = "ceph-api-test-admin"
	DashboardPass = "ceph-api-test-pass"
	RestfulUser   = "ceph-api-test-restful"

	healthCheckTimeout = 3 * time.Minute
	moduleEnableWait   = 60 * time.Second

	envCephImage     = "CEPH_TEST_IMAGE"
	defaultCephImage = "ghcr.io/arttor/ceph-test:v19"
)

var (
	networkSubnetPrefix = netip.MustParsePrefix(networkSubnet)
	networkGatewayAddr  = netip.MustParseAddr(networkGateway)
	monIPAddr           = netip.MustParseAddr(MonIP)
)

type CephEnv struct {
	tcNetwork  *testcontainers.DockerNetwork
	container  testcontainers.Container
	restfulKey string
}

func NewCephEnv(ctx context.Context) (*CephEnv, error) {
	env := &CephEnv{}

	if err := env.createNetwork(ctx); err != nil {
		return nil, err
	}
	if err := env.startContainer(ctx); err != nil {
		env.Close()
		return nil, err
	}
	if err := env.waitHealthy(ctx, healthCheckTimeout); err != nil {
		env.Close()
		return nil, err
	}
	if err := env.enableDashboard(ctx); err != nil {
		env.Close()
		return nil, fmt.Errorf("enable dashboard: %w", err)
	}
	if err := env.enableRestful(ctx); err != nil {
		env.Close()
		return nil, fmt.Errorf("enable restful: %w", err)
	}
	return env, nil
}

func (e *CephEnv) createNetwork(ctx context.Context) error {
	net, err := tcnetwork.New(ctx,
		tcnetwork.WithDriver("bridge"),
		tcnetwork.WithIPAM(&network.IPAM{
			Driver: "default",
			Config: []network.IPAMConfig{
				{Subnet: networkSubnetPrefix, Gateway: networkGatewayAddr},
			},
		}),
	)
	if err != nil {
		return fmt.Errorf("create network: %w", err)
	}
	e.tcNetwork = net
	return nil
}

func (e *CephEnv) startContainer(ctx context.Context) error {
	image := os.Getenv(envCephImage)
	if image == "" {
		image = defaultCephImage
	}

	netName := e.tcNetwork.Name

	req := testcontainers.ContainerRequest{
		Image: image,
		Env: map[string]string{
			"MON_IP":              MonIP,
			"CEPH_PUBLIC_NETWORK": cephPublicNetwork,
		},
		ExposedPorts: []string{
			fmt.Sprintf("%d/tcp", RGWPort),
			fmt.Sprintf("%d/tcp", DashboardPort),
			fmt.Sprintf("%d/tcp", RestfulPort),
		},
		Networks:       []string{netName},
		NetworkAliases: map[string][]string{netName: {"ceph"}},
		EndpointSettingsModifier: func(es map[string]*network.EndpointSettings) {
			es[netName] = &network.EndpointSettings{
				IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: monIPAddr},
				Aliases:    []string{"ceph"},
			}
		},
		HostConfigModifier: func(hc *mobycontainer.HostConfig) {
			hc.Privileged = true
		},
		ConfigModifier: func(cfg *mobycontainer.Config) {
			cfg.Hostname = "ceph-demo"
		},
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return fmt.Errorf("start ceph container: %w", err)
	}
	e.container = c
	return nil
}

func (e *CephEnv) waitHealthy(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// `ceph health` takes a few seconds to respond at all after container start.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		select {
		case <-ctx.Done():
			if lastErr == nil {
				return fmt.Errorf("timeout waiting for ceph health: %w", ctx.Err())
			}
			return fmt.Errorf("timeout waiting for ceph health (last: %s): %w", lastErr.Error(), ctx.Err())
		case <-ticker.C:
			exitCode, reader, err := e.container.Exec(ctx, []string{"ceph", "health"}, tcexec.Multiplexed())
			if err != nil {
				lastErr = err
				continue
			}
			if exitCode == 0 {
				return nil
			}
			out, _ := io.ReadAll(reader)
			lastErr = fmt.Errorf("exit %d: %s", exitCode, bytes.TrimSpace(out))
		}
	}
}

func (e *CephEnv) enableDashboard(ctx context.Context) error {
	if err := e.execOK(ctx, []string{"ceph", "mgr", "module", "enable", "dashboard"}); err != nil {
		return err
	}
	if err := e.execOK(ctx, []string{"ceph", "dashboard", "create-self-signed-cert"}); err != nil {
		return err
	}

	if err := e.writeFile(ctx, "/tmp/dash-pass.txt", DashboardPass); err != nil {
		return fmt.Errorf("stage dashboard password: %w", err)
	}
	// Ignore: rerun against a reused container hits "user already exists".
	_ = e.execLog(ctx, []string{
		"ceph", "dashboard", "ac-user-create",
		"--enabled", "--force-password",
		DashboardUser, "-i", "/tmp/dash-pass.txt", "administrator",
	})

	return e.waitMgrService(ctx, "dashboard")
}

func (e *CephEnv) enableRestful(ctx context.Context) error {
	if err := e.execOK(ctx, []string{"ceph", "mgr", "module", "enable", "restful"}); err != nil {
		return err
	}
	if err := e.execOK(ctx, []string{"ceph", "restful", "create-self-signed-cert"}); err != nil {
		return err
	}

	exitCode, reader, err := e.container.Exec(ctx, []string{"ceph", "restful", "create-key", RestfulUser}, tcexec.Multiplexed())
	if err != nil {
		return fmt.Errorf("create-key: %w", err)
	}
	out, _ := io.ReadAll(reader)
	if exitCode != 0 {
		return fmt.Errorf("create-key exit %d: %s", exitCode, bytes.TrimSpace(out))
	}
	e.restfulKey = strings.TrimSpace(string(out))

	return e.waitMgrService(ctx, "restful")
}

// waitMgrService polls `ceph mgr services` until the named module reports a
// listening URL. The module can be config-active before the socket binds.
func (e *CephEnv) waitMgrService(ctx context.Context, moduleName string) error {
	deadline := time.Now().Add(moduleEnableWait)
	for time.Now().Before(deadline) {
		exitCode, reader, err := e.container.Exec(ctx, []string{"ceph", "mgr", "services"}, tcexec.Multiplexed())
		if err == nil && exitCode == 0 {
			out, _ := io.ReadAll(reader)
			if bytes.Contains(out, []byte(`"`+moduleName+`":`)) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("mgr module %s did not report a service URL within %s", moduleName, moduleEnableWait)
}

func (e *CephEnv) execOK(ctx context.Context, cmd []string) error {
	exitCode, reader, err := e.container.Exec(ctx, cmd, tcexec.Multiplexed())
	if err != nil {
		return fmt.Errorf("exec %v: %w", cmd, err)
	}
	if exitCode != 0 {
		out, _ := io.ReadAll(reader)
		return fmt.Errorf("exec %v: exit %d: %s", cmd, exitCode, bytes.TrimSpace(out))
	}
	return nil
}

func (e *CephEnv) execLog(ctx context.Context, cmd []string) error {
	exitCode, _, err := e.container.Exec(ctx, cmd, tcexec.Multiplexed())
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("exec %v: exit %d", cmd, exitCode)
	}
	return nil
}

func (e *CephEnv) writeFile(ctx context.Context, path, content string) error {
	return e.execOK(ctx, []string{"sh", "-c", fmt.Sprintf("cat > %s <<'EOF'\n%s\nEOF\n", path, content)})
}

func (e *CephEnv) Container() testcontainers.Container {
	return e.container
}

// MappedURL returns an http/https URL on the host that maps to the given
// container port.
func (e *CephEnv) MappedURL(ctx context.Context, scheme string, port int) (string, error) {
	host, err := e.container.Host(ctx)
	if err != nil {
		return "", fmt.Errorf("get container host: %w", err)
	}
	mp, err := e.container.MappedPort(ctx, fmt.Sprintf("%d/tcp", port))
	if err != nil {
		return "", fmt.Errorf("get mapped port %d: %w", port, err)
	}
	return fmt.Sprintf("%s://%s:%s", scheme, host, mp.Port()), nil
}

// ReloadDashboard disables and re-enables the dashboard mgr module so its
// in-memory access-control DB is reloaded from mgr/dashboard/accessdb_v2.
// Use after writing users/roles to that key from outside the dashboard
// (e.g. ceph-api's bootstrap admin) to defeat the dashboard's startup-time
// user-table cache.
func (e *CephEnv) ReloadDashboard(ctx context.Context) error {
	if err := e.execOK(ctx, []string{"ceph", "mgr", "module", "disable", "dashboard"}); err != nil {
		return fmt.Errorf("disable dashboard: %w", err)
	}
	if err := e.execOK(ctx, []string{"ceph", "mgr", "module", "enable", "dashboard"}); err != nil {
		return fmt.Errorf("enable dashboard: %w", err)
	}
	if err := e.waitMgrService(ctx, "dashboard"); err != nil {
		return err
	}
	return e.waitDashboardListening(ctx)
}

func (e *CephEnv) waitDashboardListening(ctx context.Context) error {
	host, err := e.container.Host(ctx)
	if err != nil {
		return fmt.Errorf("get container host: %w", err)
	}
	mp, err := e.container.MappedPort(ctx, fmt.Sprintf("%d/tcp", DashboardPort))
	if err != nil {
		return fmt.Errorf("get mapped port %d: %w", DashboardPort, err)
	}
	addr := host + ":" + mp.Port()
	deadline := time.Now().Add(moduleEnableWait)
	var dialer net.Dialer
	for time.Now().Before(deadline) {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("dashboard %s not listening within %s", addr, moduleEnableWait)
}

// Dashboard returns the dashboard HTTPS URL and administrator credentials.
// The cert is self-signed; callers must skip verification.
func (e *CephEnv) Dashboard(ctx context.Context) (url, user, pass string, err error) {
	u, err := e.MappedURL(ctx, "https", DashboardPort)
	if err != nil {
		return "", "", "", err
	}
	return u, DashboardUser, DashboardPass, nil
}

func (e *CephEnv) Restful(ctx context.Context) (url, user, key string, err error) {
	u, err := e.MappedURL(ctx, "https", RestfulPort)
	if err != nil {
		return "", "", "", err
	}
	return u, RestfulUser, e.restfulKey, nil
}

func (e *CephEnv) RGW(ctx context.Context) (string, error) {
	return e.MappedURL(ctx, "http", RGWPort)
}

// CephConfig copies ceph.conf + admin keyring from the container into a tmp
// dir on the host, rewriting the global keyring path so a host-side process
// can use the conf directly. Caller must os.RemoveAll(dir) when done.
func (e *CephEnv) CephConfig(ctx context.Context) (string, error) {
	dir, err := os.MkdirTemp("", "ceph-conf-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}

	keyringPath := filepath.Join(dir, "ceph.client.admin.keyring")
	if err := e.copyFileFromContainer(ctx, "/etc/ceph/ceph.client.admin.keyring", keyringPath); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("copy keyring: %w", err)
	}

	conf, err := e.readFileFromContainer(ctx, "/etc/ceph/ceph.conf")
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("read ceph.conf: %w", err)
	}

	rewritten := rewriteKeyringPath(conf, keyringPath)
	confPath := filepath.Join(dir, "ceph.conf")
	if err := os.WriteFile(confPath, []byte(rewritten), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("write ceph.conf: %w", err)
	}

	return dir, nil
}

func (e *CephEnv) readFileFromContainer(ctx context.Context, containerPath string) (string, error) {
	rc, err := e.container.CopyFileFromContainer(ctx, containerPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (e *CephEnv) copyFileFromContainer(ctx context.Context, containerPath, hostPath string) error {
	content, err := e.readFileFromContainer(ctx, containerPath)
	if err != nil {
		return err
	}
	return os.WriteFile(hostPath, []byte(content), 0o600)
}

func rewriteKeyringPath(conf, keyringPath string) string {
	var out strings.Builder
	section := ""
	foundInGlobal := false
	for _, line := range strings.Split(conf, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = trimmed
		}
		// Don't match keyring_dir and friends.
		isKeyring := (strings.HasPrefix(trimmed, "keyring =") || strings.HasPrefix(trimmed, "keyring=")) &&
			!strings.HasPrefix(trimmed, "keyring_")
		if isKeyring && section == "[global]" {
			out.WriteString("\tkeyring = " + keyringPath + "\n")
			foundInGlobal = true
			continue
		}
		out.WriteString(line + "\n")
	}
	result := strings.TrimRight(out.String(), "\n") + "\n"
	if !foundInGlobal {
		result = strings.Replace(result, "[global]\n", "[global]\n\tkeyring = "+keyringPath+"\n", 1)
	}
	return result
}

func (e *CephEnv) Close() {
	ctx := context.Background()
	if e.container != nil {
		_ = e.container.Terminate(ctx)
	}
	if e.tcNetwork != nil {
		_ = e.tcNetwork.Remove(ctx)
	}
}
