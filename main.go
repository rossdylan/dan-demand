package main

import (
	"flag"
	"time"

	"github.com/golang/glog"
	"github.com/pkg/errors"
)

const (
	slackEventTimeout = 3 * time.Second
)

var (
	flagConfigPath = flag.String("dan-demand.config", "", "Configuration file location")
)

func main() {
	flag.Parse()

	if *flagConfigPath == "" {
		glog.Fatal("please specify a config file")
	}

	config, err := LoadConfig(*flagConfigPath)
	if err != nil {
		glog.Fatal(errors.Wrap(err, "failed to load config: "))
	}

	engine, err := NewEngine(config)
	if err != nil {
		glog.Fatal(errors.Wrap(err, "failed to create Engine: "))
	}

	glog.Infof("DanDemand running on %s", config.Server.Address)
	err = startZPages(config.Server.ZPagesAddress)
	if err != nil {
		glog.Fatal(errors.Wrap(err, "failed to start zpages: "))
	}
	glog.Fatal(engine.ListenAndServe())
}
