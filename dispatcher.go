package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/nlopes/slack/slackevents"
	"github.com/pkg/errors"
)

type eventHandlerFunc func(ctx context.Context, event interface{}) error
type callbackHandlerFunc func(ctx context.Context, event interface{}) error

var (
	errInvalidEvent         = errors.New("invalid event passed to handler")
	errInvalidCallbackEvent = errors.New("invalid CallbackEvent passed to handler")
)

// SlackEventDispatcher is an http.Handler implementor that attempts to dispatch slack events to
// the handlers that are set for them. It also handles URL verification automatically so you don't
// have to worry about it.
type SlackEventDispatcher struct {
	config SlackConfig

	// eventHandlers stores the mappings of top level events to functions that handle them
	// map[string]eventHandlerFunc
	eventHandlers *sync.Map

	// callbackHandlers stores the mappings of
	// map[string]eventHandlerFunc
	callbackHandlers *sync.Map
}

func NewSlackEventDispatcher(config SlackConfig) *SlackEventDispatcher {
	return &SlackEventDispatcher{
		config:           config,
		eventHandlers:    &sync.Map{},
		callbackHandlers: &sync.Map{},
	}
}

// SetEventHandler sets the handler for a given top level slack event. There can only be one handler
// per event at a time.
func (sed *SlackEventDispatcher) SetEventHandler(etype string, handler eventHandlerFunc) {
	glog.V(2).Infof("setting event handler '%s' -> %#v", etype, handler)
	sed.eventHandlers.Store(etype, handler)
}

// SetCallbackHandler sets the handler for a given callback type. There can only be one handler per
// callback at a time.
func (sed *SlackEventDispatcher) SetCallbackHandler(ctype string, handler callbackHandlerFunc) {
	glog.V(2).Infof("setting callback handler '%s' -> %#v", ctype, handler)
	sed.callbackHandlers.Store(ctype, handler)
}

// handleURLVerification is a special case where we need to decode the body differently so we set
// this event handler up manually
func (sed *SlackEventDispatcher) handleURLVerification(body []byte) ([]byte, error) {
	var resp *slackevents.ChallengeResponse
	err := json.Unmarshal(body, &resp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal challenge body: ")
	}
	return []byte(resp.Challenge), nil
}

func (sed *SlackEventDispatcher) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithDeadline(req.Context(), time.Now().Add(time.Second*2))
	defer cancel()

	var buf bytes.Buffer
	buf.ReadFrom(req.Body)
	apiEvent, err := slackevents.ParseEvent(
		json.RawMessage(buf.String()),
		slackevents.OptionVerifyToken(
			&slackevents.TokenComparator{VerificationToken: sed.config.VerificationToken},
		),
	)
	if err != nil {
		glog.Error(errors.Wrap(err, "failed to parse event: %v"))
		resp.WriteHeader(http.StatusInternalServerError)
		return
	}

	var body []byte
	var handlerErr error

	switch apiEvent.Type {
	case slackevents.URLVerification:
		body, handlerErr = sed.handleURLVerification(buf.Bytes())
		resp.Header().Set("Content-Type", "text")
	case slackevents.CallbackEvent:
		inner := apiEvent.InnerEvent
		if handler, ok := sed.callbackHandlers.Load(inner.Type); ok {
			handlerErr = errors.Wrapf(
				handler.(callbackHandlerFunc)(ctx, inner.Data),
				"failed to execute CallbackEvent handler for '%s': ",
				inner.Type,
			)
		} else {
			glog.Infof("no callback handler for %#v", inner.Type)
		}
	default:
		if handler, ok := sed.eventHandlers.Load(apiEvent.Type); ok {
			handlerErr = errors.Wrapf(
				handler.(eventHandlerFunc)(ctx, apiEvent),
				"failed to execute Event handler for '%s': ",
				apiEvent.Type,
			)
		} else {
			glog.Infof("no event handler for %#v", apiEvent.Type)
		}
	}
	if handlerErr != nil {
		glog.Error(errors.Wrap(handlerErr, "failed to dispatch slack events: "))
		resp.WriteHeader(http.StatusInternalServerError)
	}
	if len(body) > 0 {
		resp.Write(body)
	}
}
