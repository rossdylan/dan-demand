package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/nlopes/slack"
	"github.com/nlopes/slack/slackevents"
	"github.com/pkg/errors"
)

const (
	slackEventTimeout = 3 * time.Second
)

var (
	flagConfigPath = flag.String("dan-demand.config", "", "Configuration file location")
	flagHTTPAddr   = flag.String("dan-demand.address", "127.0.0.1:8080", "Address to serve dan-demand on")
)

type server struct {
	slackSecret    string
	twilioClient   *twilioClient
	slackClient    *slack.Client
	slackAppClient *slack.Client

	// These are used to build a strings.Replacer that will autoreplace all UIDs with usernames
	replacerLock sync.RWMutex
	userMap      map[string]string
	userReplacer *strings.Replacer

	// tracker is used to keep track of the full message metadata for messages that mention our bot
	tracker *MessageTracker
}

func (s *server) replaceUIDs(text string) string {
	s.replacerLock.RLock()
	defer s.replacerLock.RUnlock()
	return s.userReplacer.Replace(text)
}

func (s *server) lookupUserName(ctx context.Context, uid string) (string, error) {
	s.replacerLock.RLock()
	val, ok := s.userMap[uid]
	s.replacerLock.RUnlock()
	if ok {
		return val, nil
	}
	user, err := s.slackClient.GetUserInfoContext(ctx, uid)
	if err != nil {
		return "", errors.Wrap(err, "failed to lookup user info: ")
	}
	s.replacerLock.Lock()
	defer s.replacerLock.Unlock()
	s.userMap[uid] = user.Name
	var pairs []string
	for k, v := range s.userMap {
		pairs = append(pairs, k, v)
	}
	glog.Infof("loaded uid replacer with %d replacements", len(s.userMap))
	s.userReplacer = strings.NewReplacer(pairs...)
	return user.Name, nil
}

func (s *server) handleChallenge(body []byte) ([]byte, int) {
	var resp *slackevents.ChallengeResponse
	err := json.Unmarshal(body, &resp)
	if err != nil {
		glog.Errorf("failed to unmarshal challenge body: %v", err)
		return nil, http.StatusInternalServerError
	}
	return []byte(resp.Challenge), http.StatusOK
}

func (s *server) handleMention(ctx context.Context, event *slackevents.AppMentionEvent) {
	name, err := s.lookupUserName(ctx, event.User)
	if err != nil {
		glog.Errorf("failed to lookup user name: %v", err)
	}

	if name == "" {
		name = "unknown"
	}

	params := sendMessageParams{
		Message: name + ": " + s.replaceUIDs(event.Text),
	}
	msgData, ok := s.tracker.WaitForMessage(ctx, event.Channel, event.TimeStamp)
	if ok {
		if len(msgData.Files) > 0 {
			// only take the first one
			// TODO(rossdylan): See if we can add multiple files
			if msgData.Files[0].IsPublic {
				file, _, _, err := s.slackAppClient.ShareFilePublicURLContext(ctx, msgData.Files[0].ID)
				if err != nil {
					glog.Error(errors.Wrap(err, "failed to share mms file: "))
				} else {
					publicURLParts := strings.Split(file.PermalinkPublic, "-")

					downloadURL := file.URLPrivateDownload + "?pub_secret=" + publicURLParts[len(publicURLParts)-1]
					params.MediaURL = &downloadURL
				}
			}
		}
	}

	if err := s.twilioClient.SendMessage(ctx, params); err != nil {
		go func() {
			reactErr := s.slackClient.AddReactionContext(
				context.Background(),
				"thumbsdown",
				slack.ItemRef{
					Channel:   event.Channel,
					Timestamp: event.TimeStamp,
				},
			)
			if reactErr != nil {
				glog.Error(errors.Wrap(reactErr, "failed to add failure react: "))
			}
		}()
		glog.Errorf("failed to send sms: %v", err)
	}
	go func() {
		reactErr := s.slackClient.AddReactionContext(
			context.Background(),
			"thumbsup",
			slack.ItemRef{
				Channel:   event.Channel,
				Timestamp: event.TimeStamp,
			},
		)
		if reactErr != nil {
			glog.Error(errors.Wrap(reactErr, "failed to add success react: "))
		}
	}()
}

// HandleEvents is the main handler for slack events. It reads in and parses the slack event and
// tries to dispatch the events it cares about to seperate handlers
func (s *server) HandleEvents(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	buf.ReadFrom(r.Body)
	apiEvent, err := slackevents.ParseEvent(
		json.RawMessage(buf.String()),
		slackevents.OptionVerifyToken(
			&slackevents.TokenComparator{VerificationToken: s.slackSecret},
		),
	)
	if err != nil {
		glog.Errorf("failed to parse event: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var data []byte
	// We default to StatusOK in order to avoid slack retrying. We we don't really care about since
	// if it failed it was probably a bad request in some way
	var status = http.StatusOK

	switch apiEvent.Type {
	// URLVerification is for app setup and is how slack knows we are actaully the app backend
	case slackevents.URLVerification:
		data, status = s.handleChallenge(buf.Bytes())
		w.Header().Set("Content-Type", "text")

	// All the other events we care about go through the Callback even type
	case slackevents.CallbackEvent:
		inner := apiEvent.InnerEvent
		switch ev := inner.Data.(type) {
		// We only care about the messages where we are mentioned currently
		case *slackevents.AppMentionEvent:
			s.handleMention(r.Context(), ev)
		case *slackevents.MessageEvent:
			s.tracker.HandleMessage(r.Context(), ev)
		default:
			glog.Warningf("unknown callback event %#v", ev)
		}
	default:
		glog.Warning("unknown event")
	}
	w.WriteHeader(status)
	if data != nil {
		w.Write(data)
	}
}

func main() {
	flag.Parse()

	if *flagConfigPath == "" {
		glog.Fatal("please specify a config file")
	}

	config, err := LoadConfig(*flagConfigPath)
	if err != nil {
		glog.Fatalf("failed to load config: %v", err)
	}

	twc, err := newTwilioClient(config.Twilio)
	if err != nil {
		glog.Fatal(err)
	}

	// TODO(rossdylan): Make a proper constructor for server
	s := &server{
		slackSecret:    config.Slack.VerificationToken,
		twilioClient:   twc,
		userMap:        make(map[string]string),
		slackClient:    slack.New(config.Slack.BotToken),
		slackAppClient: slack.New(config.Slack.AppToken),
	}

	// Use the AuthTest method to grab out bot username and userid so we can do
	// translations of our own name in mentions correctly
	authResp, err := s.slackClient.AuthTest()
	if err != nil {
		glog.Fatal(err)
	}
	// We don't need a lock here since we haven't spawned any of the http routines yet
	s.lookupUserName(context.Background(), authResp.UserID)

	tracker, err := NewMessageTracker(authResp.UserID)
	if err != nil {
		glog.Fatal(err)
	}
	s.tracker = tracker

	router := mux.NewRouter()
	router.HandleFunc("/slack-events", s.HandleEvents)

	// Configure the http server ourselves inorder to configure timeouts and the listen address
	hserver := &http.Server{
		Handler:      handlers.LoggingHandler(os.Stdout, router),
		Addr:         *flagHTTPAddr,
		WriteTimeout: slackEventTimeout,
		ReadTimeout:  slackEventTimeout,
	}

	glog.Infof("serving on %s", *flagHTTPAddr)
	glog.Fatal(hserver.ListenAndServe())
}
