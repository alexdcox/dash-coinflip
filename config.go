package main

import (
	"github.com/ghodss/yaml"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
)

type Config struct {
	LogLevel         string
	MinimumThreshold float64
	MaximumThreshold float64
	DatabasePath     string
	Dash             ConfigDashNode
	Http             ConfigHttp
}

type ConfigHttp struct {
	Enabled bool
	Port    string
}

type ConfigDashNode struct {
	Network     string
	Hostport    string
	User        string
	Pass        string
	ZmqEndpoint string
	Debug       bool
}

func (c *Config) Load() error {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.yml"
	}
	logrus.Infof("Loading configuration from '%s'", configPath)

	configBytes, err := ioutil.ReadFile(configPath)
	if err != nil {
		return errors.WithStack(err)
	}

	err = yaml.Unmarshal(configBytes, c)
	return errors.WithStack(err)
}
