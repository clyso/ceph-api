package rados

import "time"

type Config struct {
	User         string        `yaml:"user"`
	UserKeyring  string        `yaml:"userKeyring"`
	MonHost      string        `yaml:"monHost"`
	RadosTimeout time.Duration `yaml:"radosTimeout"`
}
