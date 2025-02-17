package rados

import (
	"time"
)

type Config struct {
	User         string        `yaml:"user"`
	UserKeyring  string        `yaml:"userKeyring"`
	MonHost      string        `yaml:"monHost"`
	RadosTimeout time.Duration `yaml:"radosTimeout"`
	UseMock      bool          `yaml:"useMock"`
}

type RadosConnInterface interface {
	MonCommand(in []byte) (out []byte, cmdStatus string, err error)
	MonCommandWithInputBuffer(cmd []byte, in []byte) (out []byte, cmdStatus string, err error)
	MgrCommand(in [][]byte) (out []byte, cmdStatus string, err error)
	Shutdown()
}
