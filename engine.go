package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/nlopes/slack/slackevents"
	"github.com/pkg/errors"
	"go.opencensus.io/plugin/ochttp"
)

const (
	twilioMsgLimit = 1600

	kb                  = 1024
	twilioFileSizeLimit = 500 * kb
)

func chunkString(s string, chunkLen int) []string {
	var chunks []string
	for {
		if len(s) < chunkLen {
			chunks = append(chunks, s)
			break
		}
		chunks = append(chunks, s[:chunkLen])
		s = s[chunkLen:]
	}
	return chunks
}

// Engine is the main location for DanDemand application logic. It ties together the API clients,
// the http server, and the event dispatcher infrastructure
type Engine struct {
	config     *DanDemandConfig
	server     *http.Server
	dispatcher *SlackEventDispatcher

	slackWrapper *SlackWrapper
	twilioClient *TwilioClient
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
	}

	dispatcher.SetCallbackHandler(slackevents.Message, engine.HandleMessage)

	return engine, nil
}

func (e *Engine) HandleMessage(ctx context.Context, rawEvent interface{}) error {
	event := rawEvent.(*slackevents.MessageEvent)

	if !(event.ChannelType == "channel" || event.ChannelType == "mim" || event.ChannelType == "group") {
		return nil
	}
	if !strings.Contains(event.Text, e.slackWrapper.BotUID) {
		return nil
	}

	name, err := e.slackWrapper.LookupUserName(ctx, event.User)
	if err != nil {
		e.slackWrapper.AddReactionBackground("thumbsdown", event.Channel, event.TimeStamp)
		return errors.Wrapf(err, "failed to lookup username for '%s': ", event.User)
	}

	var mediaURL *string
	if len(event.Files) > 0 {
		// TODO(rossdylan): See if we can add multiple files
		if event.Files[0].IsPublic {
			if event.Files[0].Size > twilioFileSizeLimit {
				e.slackWrapper.AddReactionBackground("scronch", event.Channel, event.TimeStamp)
				return errors.Errorf(
					"mms file '%s' too large %d > %d",
					event.Files[0].Name,
					event.Files[0].Size/kb,
					twilioFileSizeLimit/kb,
				)
			}
			url, err := e.slackWrapper.ShareFilePublic(ctx, &event.Files[0])
			if err != nil {
				e.slackWrapper.AddReactionBackground("thumbsdown", event.Channel, event.TimeStamp)
				return errors.Wrap(err, "failed to create mms public link: ")
			}
			mediaURL = &url
		}
	}

	baseMessage := name + ": " + e.slackWrapper.ReplaceUIDs(event.Text)
	for index, chunk := range chunkString(baseMessage, twilioMsgLimit) {
		params := SendMessageParams{
			Message: chunk,
			Chunked: index > 0,
		}

		// Only attach our media to the first message
		if mediaURL != nil {
			params.MediaURL = mediaURL
			mediaURL = nil
		}

		if err := e.twilioClient.SendMessage(ctx, params); err != nil {
			e.slackWrapper.AddReactionBackground("thumbsdown", event.Channel, event.TimeStamp)
			return errors.Wrap(err, "failed to send message: ")
		}
	}
	var emoji string
	if len(event.Files) > 0 {
		emoji = "foot"
	} else {
		emoji = "thumbsup"
	}
	e.slackWrapper.AddReactionBackground(emoji, event.Channel, event.TimeStamp)
	return nil
}

func (e *Engine) ListenAndServe() error {
	return errors.Wrap(e.server.ListenAndServe(), "ListenAndServe failed: ")
}
