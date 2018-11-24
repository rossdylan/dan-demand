package main

import (
	"io/ioutil"

	"github.com/pelletier/go-toml"
	"github.com/pkg/errors"
)

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
	Slack  SlackConfig  `toml:"slack"`
	Twilio TwilioConfig `toml:"twilio"`
}

func LoadConfig(path string) (*DanDemandConfig, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read config file: ")
	}

	var config DanDemandConfig

	// Default to allowing 1 text per second
	config.Twilio.Limit = "1s"

	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal config")
	}
	return &config, nil
}
