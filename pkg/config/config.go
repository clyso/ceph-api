package config

import (
	"embed"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/clyso/ceph-api/pkg/api"
	"github.com/clyso/ceph-api/pkg/auth"
	"github.com/clyso/ceph-api/pkg/log"
	"github.com/clyso/ceph-api/pkg/metrics"
	"github.com/clyso/ceph-api/pkg/rados"
	"github.com/clyso/ceph-api/pkg/trace"
	"github.com/spf13/viper"
)

//go:embed config.yaml
var configFile embed.FS

type Build struct {
	Version string
	Commit  string
}

type Config struct {
	Log log.Config `yaml:"log"`

	Metrics metrics.Config `yaml:"metrics"`

	Trace trace.Config `yaml:"trace"`

	Api api.Config `yaml:"api"`

	Rados rados.Config `yaml:"rados"`

	Auth auth.Config `yaml:"auth"`

	App struct {
		CreateAdmin   bool   `yaml:"createAdmin"`
		AdminUsername string `yaml:"adminUsername"`
		AdminPassword string `yaml:"adminPassword"`
		BcryptPwdCost int    `yaml:"bcryptPwdCos"`
	} `yaml:"app"`
}

func Get(conf any, sources ...Src) error {
	data, err := configFile.Open("config.yaml")
	if err != nil {
		return fmt.Errorf("%w: unable to read default config.yaml", err)
	}
	defer func() { _ = data.Close() }()

	v := viper.NewWithOptions(viper.EnvKeyReplacer(strings.NewReplacer(".", "_")))
	v.SetConfigType("yaml")
	err = v.ReadConfig(data)
	if err != nil {
		return err
	}

	for _, source := range sources {
		switch src := source.(type) {
		case pathOpt:
			_, err = os.Stat(string(src))
			if err != nil {
				return fmt.Errorf("%w: unable read config file %q", err, string(src))
			}
			v.SetConfigFile(string(src))
			err = v.MergeInConfig()
			if err != nil {
				return fmt.Errorf("%w: unable merge config file %q", err, string(src))
			}
		case readerOpt:
			err = v.MergeConfig(src.Reader)
			if err != nil {
				return fmt.Errorf("%w: unable merge config reader", err)
			}
		}
	}

	// Override config values if there are envs
	v.AutomaticEnv()
	v.SetEnvPrefix("CFG")

	err = v.Unmarshal(&conf)
	if err != nil {
		return fmt.Errorf("%w: unable to unmarshal config", err)
	}

	return nil
}

type options struct {
	sources []any
}

type Src interface {
	apply(*options)
}

type pathOpt string

func (p pathOpt) apply(opts *options) {
	opts.sources = append(opts.sources, p)
}

func Path(path string) Src {
	return pathOpt(path)
}

type readerOpt struct {
	io.Reader
	Name string
}

func (r readerOpt) apply(opts *options) {
	opts.sources = append(opts.sources, r)
}

func Reader(reader io.Reader, name string) Src {
	return readerOpt{reader, name}
}
