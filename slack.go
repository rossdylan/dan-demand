package main

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/nlopes/slack"
	"github.com/nlopes/slack/slackevents"
	"github.com/pkg/errors"
)

// SlackWrapper is used to combine the bot api client and the app api client and expose the methods
// DanDemand actually needs in a better way
type SlackWrapper struct {
	config SlackConfig

	appClient *slack.Client
	botClient *slack.Client

	BotUID string

	// These are used to build a strings.Replacer that will autoreplace all UIDs with usernames
	replacerLock sync.RWMutex
	userMap      map[string]string
	userReplacer *strings.Replacer
}

func NewSlackWrapper(config SlackConfig) (*SlackWrapper, error) {
	wrapper := &SlackWrapper{
		config:    config,
		appClient: slack.New(config.AppToken),
		botClient: slack.New(config.BotToken),
		userMap:   make(map[string]string),
	}

	// Use the AuthTest method to grab out bot username and userid so we can do
	// translations of our own name in mentions correctly
	authResp, err := wrapper.botClient.AuthTest()
	if err != nil {
		return nil, errors.Wrap(err, "failed to authenticate bot client: ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	_, err = wrapper.LookupUserName(ctx, authResp.UserID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to lookup bot username: ")
	}
	wrapper.BotUID = authResp.UserID

	_, err = wrapper.appClient.AuthTest()
	if err != nil {
		return nil, errors.Wrap(err, "failed to authenticate app client: ")
	}
	return wrapper, nil
}

// ReplaceUIDs attempts to replace all the UIDs we know about with their usernames.
func (sw *SlackWrapper) ReplaceUIDs(text string) string {
	sw.replacerLock.RLock()
	defer sw.replacerLock.RUnlock()
	return sw.userReplacer.Replace(text)
}

// LookupUserName is used to populate our mapping of UID to Username. It also rebuilds the
// internal strings.Replacer to include the new user name.
// TODO(rossdylan): Look into doing the Replacer refresh in a goroutine to avoid blocking for a
// long time on huge maps
func (sw *SlackWrapper) LookupUserName(ctx context.Context, uid string) (string, error) {
	sw.replacerLock.RLock()
	val, ok := sw.userMap[uid]
	sw.replacerLock.RUnlock()
	if ok {
		return val, nil
	}

	user, err := sw.appClient.GetUserInfoContext(ctx, uid)
	if err != nil {
		return "", errors.Wrap(err, "failed to lookup user info: ")
	}

	sw.replacerLock.Lock()
	defer sw.replacerLock.Unlock()
	sw.userMap[uid] = user.Name
	var pairs []string
	for k, v := range sw.userMap {
		pairs = append(pairs, k, v)
	}
	glog.Infof("loaded uid replacer with %d replacements", len(sw.userMap))
	sw.userReplacer = strings.NewReplacer(pairs...)
	return user.Name, nil
}

// ShareFilePublic is used to publically share a file and generate a direct link to it.
// NOTE(rossdylan): This is a bit dangerous since it requires the App level client and has access to
// all files in the workspace.
func (sw *SlackWrapper) ShareFilePublic(ctx context.Context, file *slackevents.File) (string, error) {
	slackFile, _, _, err := sw.appClient.ShareFilePublicURLContext(ctx, file.ID)
	if err != nil {
		return "", errors.Wrapf(err, "failed to share file '%s': ", slackFile.Name)
	}
	publicURLParts := strings.Split(slackFile.PermalinkPublic, "-")
	// NOTE(rossdylan) HAX Alert, files are defined with 3 '-' seperate identifiers, in the public
	// URLs the last segment is the public secret used to access the private file link. We have to
	// do it this way since the File structures don't provide a public direct download link
	return slackFile.URLPrivateDownload + "?pub_secret=" + publicURLParts[len(publicURLParts)-1], nil
}

// AddReaction adds an emoji reaction to the given reference
func (sw *SlackWrapper) AddReaction(ctx context.Context, emoji, channel, timestamp string) error {
	ref := slack.ItemRef{Channel: channel, Timestamp: timestamp}
	err := sw.botClient.AddReactionContext(
		ctx,
		emoji,
		ref,
	)
	return errors.Wrapf(err, "failed to add reaction to '%#v': ", ref)
}

func (sw *SlackWrapper) AddReactionBackground(emoji, channel, timestamp string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := sw.AddReaction(ctx, emoji, channel, timestamp)
		if err != nil {
			glog.Error(err)
		}
	}()
}
