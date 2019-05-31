package main

import (
	"io/ioutil"
	"os"

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

func (sec *ServerConfig) InitFromEnv() {
	sec.Address = os.Getenv("SERVER_ADDR")
	sec.ZPagesAddress = os.Getenv("ZPAGES_ADDR")
}

type SlackConfig struct {
	BotToken          string `toml:"bot_token"`
	AppToken          string `toml:"app_token"`
	VerificationToken string `toml:"verification_token"`
	RefreshInterval   string `toml:"refresh_interval"`
}

func (slc *SlackConfig) InitFromEnv() {
	slc.BotToken = os.Getenv("SLACK_BOT_TOKEN")
	slc.AppToken = os.Getenv("SLACK_APP_TOKEN")
	slc.VerificationToken = os.Getenv("SLACK_VERIF_TOKEN")
	slc.RefreshInterval = os.Getenv("SLACK_REFRESH_INTERVAL")
}

type TwilioConfig struct {
	SID        string `toml:"account_sid"`
	Token      string `toml:"token"`
	ToNumber   string `toml:"to_number"`
	FromNumber string `toml:"from_number"`
	Limit      string `toml:"rate_limit"`
}

func (tc *TwilioConfig) InitFromEnv() {
	tc.SID = os.Getenv("TWILIO_SID")
	tc.Token = os.Getenv("TWILIO_TOKEN")
	tc.ToNumber = os.Getenv("TWILIO_TO_NUMBER")
	tc.FromNumber = os.Getenv("TWILIO_FROM_NUMBER")
	tc.Limit = os.Getenv("TWILIO_LIMIT")
}

type DanDemandConfig struct {
	Server *ServerConfig `toml:"server"`
	Slack  *SlackConfig  `toml:"slack"`
	Twilio *TwilioConfig `toml:"twilio"`
}

func (ddc *DanDemandConfig) InitFromEnv() {
	ddc.Server = &ServerConfig{}
	ddc.Server.InitFromEnv()

	ddc.Slack = &SlackConfig{}
	ddc.Slack.InitFromEnv()

	ddc.Twilio = &TwilioConfig{}
	ddc.Twilio.InitFromEnv()
}

func LoadConfig(path string) (*DanDemandConfig, error) {
	config := &DanDemandConfig{}
	config.InitFromEnv()

	if path != "" {
		data, err := ioutil.ReadFile(path)
		if err != nil {
			return nil, errors.Wrap(err, "failed to read config file: ")
		}

		if err := toml.Unmarshal(data, config); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal config")
		}
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
	return config, nil
}
