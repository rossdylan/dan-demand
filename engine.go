package main

import (
	"context"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/nlopes/slack/slackevents"
	"github.com/pkg/errors"
	"go.opencensus.io/plugin/ochttp"
)

// Engine is the main location for DanDemand application logic. It ties together the API clients,
// the http server, and the event dispatcher infrastructure
type Engine struct {
	config     *DanDemandConfig
	server     *http.Server
	dispatcher *SlackEventDispatcher

	slackWrapper *SlackWrapper
	twilioClient *TwilioClient
	tracker      *MessageTracker
}

func NewEngine(config *DanDemandConfig) (*Engine, error) {
	dispatcher := NewSlackEventDispatcher(config.Slack)

	slackWrapper, err := NewSlackWrapper(config.Slack)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create SlackWrapper: ")
	}

	twilioClient, err := NewTwilioClient(config.Twilio)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create TwilioClient: ")
	}

	tracker, err := NewMessageTracker(slackWrapper.BotUID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create MessageTracker: ")
	}

	// Configure out mux
	router := mux.NewRouter()
	router.Handle("/slack-events", dispatcher)
	// TODO(rossdylan): Look into adding callbacks for twilio
	// router.Handle("/twilio-callbacks", twilioClient)

	server := &http.Server{
		Handler:      &ochttp.Handler{Handler: router},
		Addr:         config.Server.Address,
		WriteTimeout: slackEventTimeout,
		ReadTimeout:  slackEventTimeout,
	}

	engine := &Engine{
		config:       config,
		server:       server,
		dispatcher:   dispatcher,
		slackWrapper: slackWrapper,
		twilioClient: twilioClient,
		tracker:      tracker,
	}

	// Forward Message events to the message tracker
	dispatcher.SetCallbackHandler(slackevents.Message, tracker.HandleMessage)

	// The main entrypoint for dan-demand is the AppMentionEvent so dispatch it to our engine handler
	// TODO(rossdylan): This is kind of a roundabout way of doing things, maybe just use the MessageEvent
	dispatcher.SetCallbackHandler(slackevents.AppMention, engine.HandleMention)

	return engine, nil
}

func (e *Engine) HandleMention(ctx context.Context, rawEvent interface{}) error {
	event := rawEvent.(*slackevents.AppMentionEvent)
	name, err := e.slackWrapper.LookupUserName(ctx, event.User)
	if err != nil {
		e.slackWrapper.AddReactionBackground("thumbsdown", event.Channel, event.TimeStamp)
		return errors.Wrapf(err, "failed to lookup username for '%s': ", event.User)
	}

	params := SendMessageParams{
		Message: name + ": " + e.slackWrapper.ReplaceUIDs(event.Text),
	}
	msgData, ok := e.tracker.WaitForMessage(ctx, event.Channel, event.TimeStamp)
	if ok {
		if len(msgData.Files) > 0 {
			// TODO(rossdylan): See if we can add multiple files
			if msgData.Files[0].IsPublic {
				url, err := e.slackWrapper.ShareFilePublic(ctx, &msgData.Files[0])
				if err != nil {
					e.slackWrapper.AddReactionBackground("thumbsdown", event.Channel, event.TimeStamp)
					return errors.Wrap(err, "failed to create mms public link: ")
				}
				params.MediaURL = &url
			}
		}
	}

	if err := e.twilioClient.SendMessage(ctx, params); err != nil {
		e.slackWrapper.AddReactionBackground("thumbsdown", event.Channel, event.TimeStamp)
		return errors.Wrap(err, "failed to send message: ")
	}
	e.slackWrapper.AddReactionBackground("thumbsup", event.Channel, event.TimeStamp)
	return nil
}

func (e *Engine) ListenAndServe() error {
	return errors.Wrap(e.server.ListenAndServe(), "ListenAndServe failed: ")
}
