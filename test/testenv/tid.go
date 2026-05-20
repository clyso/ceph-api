package testenv

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/testcontainers/testcontainers-go"
)

type DockerTestConfig struct {
	TestPkg        string
	DefaultTimeout string
	ForwardFlags   []string
	ForwardEnv     []string
}

// RunInDocker builds test/Dockerfile, mounts the repo + go mod cache +
// docker.sock, and runs `go test` against TestPkg inside the container.
// docker.sock is shared so inner tests can still spawn sibling containers.
func RunInDocker(cfg DockerTestConfig) int {
	ctx := context.Background()

	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tid: find repo root: %v\n", err)
		return 1
	}

	modCache, err := goModCache()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tid: %v\n", err)
		return 1
	}

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    filepath.Join(repoRoot, "test"),
			Dockerfile: "Dockerfile",
			KeepImage:  true,
		},
		Cmd: []string{"sleep", "infinity"},
		HostConfigModifier: func(hc *dockercontainer.HostConfig) {
			// Host networking so the inner test process can reach the
			// CephEnv container's mapped host ports.
			hc.NetworkMode = "host"
			hc.Binds = []string{
				repoRoot + ":/src:ro",
				modCache + ":/go/pkg/mod:ro",
				"/var/run/docker.sock:/var/run/docker.sock",
			}
			hc.Mounts = append(hc.Mounts, mount.Mount{
				Type:   mount.TypeVolume,
				Source: "ceph-api-build-cache",
				Target: "/root/.cache/go-build",
			})
		},
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "tid: start container: %v\n", err)
		return 1
	}
	defer func() { _ = container.Terminate(ctx) }()

	cmd := buildInnerCmd(cfg)

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Fprintf(os.Stderr, "tid: docker client: %v\n", err)
		return 1
	}
	defer func() { _ = cli.Close() }()

	containerID := container.GetContainerID()

	var envVars []string
	for _, key := range cfg.ForwardEnv {
		if val, ok := os.LookupEnv(key); ok {
			envVars = append(envVars, key+"="+val)
		}
	}

	execResp, err := cli.ContainerExecCreate(ctx, containerID, dockercontainer.ExecOptions{
		Cmd:          cmd,
		Env:          envVars,
		AttachStdout: true,
		AttachStderr: true,
		WorkingDir:   "/src",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "tid: exec create: %v\n", err)
		return 1
	}

	attachResp, err := cli.ContainerExecAttach(ctx, execResp.ID, dockercontainer.ExecAttachOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "tid: exec attach: %v\n", err)
		return 1
	}
	defer attachResp.Close()

	_, _ = stdcopy.StdCopy(os.Stdout, os.Stderr, attachResp.Reader)

	inspectResp, err := cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tid: exec inspect: %v\n", err)
		return 1
	}
	return inspectResp.ExitCode
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}

func goModCache() (string, error) {
	out, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMODCACHE: %w", err)
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return "", fmt.Errorf("GOMODCACHE is empty")
	}
	return p, nil
}

func buildInnerCmd(cfg DockerTestConfig) []string {
	cmd := []string{"go", "test", "-count=1", cfg.TestPkg}

	allowed := make(map[string]bool, len(cfg.ForwardFlags))
	for _, f := range cfg.ForwardFlags {
		allowed[f] = true
	}
	allowed["test.run"] = true
	allowed["test.timeout"] = true
	allowed["test.v"] = true

	flag.Visit(func(f *flag.Flag) {
		if !allowed[f.Name] {
			return
		}
		switch f.Name {
		case "test.run":
			cmd = append(cmd, "-run", f.Value.String())
		case "test.bench":
			cmd = append(cmd, "-bench", f.Value.String())
		case "test.benchmem":
			if f.Value.String() == "true" {
				cmd = append(cmd, "-benchmem")
			}
		case "test.timeout":
			cmd = append(cmd, "-timeout", f.Value.String())
		case "test.v":
			if f.Value.String() == "true" {
				cmd = append(cmd, "-v")
			}
		}
	})

	hasTimeout := false
	for _, a := range cmd {
		if strings.HasPrefix(a, "-timeout") {
			hasTimeout = true
			break
		}
	}
	if !hasTimeout {
		timeout := cfg.DefaultTimeout
		if timeout == "" {
			timeout = "15m"
		}
		cmd = append(cmd, "-timeout", timeout)
	}

	return cmd
}
