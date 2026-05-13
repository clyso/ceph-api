package main

import (
	"os"

	stdlog "github.com/rs/zerolog/log"
)

// this information will be collected when built, by -ldflags="-X 'main.version=$(tag)' -X 'main.commit=$(commit)'".
var (
	version = "development"
	commit  = "not set"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		stdlog.Err(err).Msg("critical error. Shutdown application")
		os.Exit(1)
	}
}
