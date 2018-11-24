package main

import (
	"io/ioutil"

	"github.com/pelletier/go-toml"
	"github.com/pkg/errors"
)

const (
	defaultServerAddress = "127.0.0.1:8080"
	defaultZPagesAddress = "127.0.0.1:8081"
	defaultTwilioLimit   = "1s"
)

type ServerConfig struct {
	Address       string `toml:"address"`
	ZPagesAddress string `toml:"zpages_address"`
}

type SlackConfig struct {
	BotToken          string `toml:"bot_token"`
	AppToken          string `toml:"app_token"`
	VerificationToken string `toml:"verification_token"`
}

type TwilioConfig struct {
	SID        string `toml:"account_sid"`
	Token      string `toml:"token"`
	ToNumber   string `toml:"to_number"`
	FromNumber string `toml:"from_number"`
	Limit      string `toml:"rate_limit"`
}

type DanDemandConfig struct {
	Server ServerConfig `toml:"server"`
	Slack  SlackConfig  `toml:"slack"`
	Twilio TwilioConfig `toml:"twilio"`
}

func LoadConfig(path string) (*DanDemandConfig, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read config file: ")
	}

	var config DanDemandConfig

	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal config")
	}

	if config.Server.Address == "" {
		config.Server.Address = defaultServerAddress
	}
	if config.Server.ZPagesAddress == "" {
		config.Server.ZPagesAddress = defaultZPagesAddress
	}
	if config.Twilio.Limit == "" {
		config.Twilio.Limit = defaultTwilioLimit
	}
	return &config, nil
}
