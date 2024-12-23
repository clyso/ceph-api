package rados

type Config struct {
	User         string  `yaml:"user"`
	UserKeyring  string  `yaml:"userKeyring"`
	MonHost      string  `yaml:"monHost"`
	RadosTimeout float64 `yaml:"radosTimeout"`
}
