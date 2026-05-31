package test

import (
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/clyso/ceph-api/test/testenv"
)

var tid = flag.Bool("tid", false, "run e2e tests inside a Docker container with CGo + ceph dev libs")

func TestMain(m *testing.M) {
	flag.Parse()
	if *tid {
		os.Exit(testenv.RunInDocker(testenv.DockerTestConfig{
			TestPkg:        "./test/...",
			DefaultTimeout: "15m",
			ForwardEnv:     []string{"CEPH_TEST_IMAGE"},
		}))
	}
	exitCode, err := runSetup(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e setup:", err)
		os.Exit(1)
	}
	os.Exit(exitCode)
}
