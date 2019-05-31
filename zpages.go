package main

import (
	"net/http"
	"net/http/pprof"

	"contrib.go.opencensus.io/exporter/prometheus"
	"github.com/golang/glog"
	"github.com/pkg/errors"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/zpages"
)

func startZPages(addr string) error {
	prom, err := prometheus.NewExporter(prometheus.Options{})
	if err != nil {
		errors.Wrap(err, "failed to create prometheus exporter")
	}

	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", prom)
		// Manually configure the pprof endpoints since we want to serve prom/zpages from the same
		// mux as well
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		zpages.Handle(mux, "/debug")
		glog.Infof("starting zpages on http://%s", addr)
		glog.Fatal(http.ListenAndServe(addr, mux))
	}()

	view.RegisterExporter(prom)
	if err := view.Register(ochttp.DefaultServerViews...); err != nil {
		return errors.Wrap(err, "failed to register ochttp views: ")
	}
	return nil
}
