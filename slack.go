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

	refreshInterval time.Duration

	appClient *slack.Client
	botClient *slack.Client

	BotUID string

	// These are used to build a strings.Replacer that will autoreplace all UIDs with usernames
	replacerLock sync.RWMutex
	userMap      map[string]string
	userReplacer *strings.Replacer
}

func NewSlackWrapper(config SlackConfig) (*SlackWrapper, error) {
	refreshInterval, err := time.ParseDuration(config.RefreshInterval)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse refresh_interval")
	}

	wrapper := &SlackWrapper{
		config:          config,
		refreshInterval: refreshInterval,
		appClient:       slack.New(config.AppToken),
		botClient:       slack.New(config.BotToken),
		userMap:         make(map[string]string),
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

	// Start our user refresh system
	go wrapper.userRefresher()

	return wrapper, nil
}

func largestThumbnailLink(f slackevents.File) (string, error) {
	if f.Thumb1024 != "" {
		return f.Thumb1024, nil
	}
	if f.Thumb960 != "" {
		return f.Thumb960, nil
	}
	if f.Thumb720 != "" {
		return f.Thumb720, nil
	}
	if f.Thumb480 != "" {
		return f.Thumb480, nil
	}
	if f.Thumb360 != "" {
		return f.Thumb360, nil
	}
	if f.Thumb160 != "" {
		return f.Thumb160, nil
	}
	if f.Thumb80 != "" {
		return f.Thumb80, nil
	}
	if f.Thumb64 != "" {
		return f.Thumb64, nil
	}
	return "", errors.New("no thumbnails found")
}

// ReplaceUIDs attempts to replace all the UIDs we know about with their usernames.
func (sw *SlackWrapper) ReplaceUIDs(text string) string {
	sw.replacerLock.RLock()
	defer sw.replacerLock.RUnlock()
	return sw.userReplacer.Replace(text)
}

// userRefresher is a background thread that periodically scrapes the list of all users to load our
// replacer.
func (sw *SlackWrapper) userRefresher() {
	ticker := time.NewTicker(sw.refreshInterval)
	for range ticker.C {
		requestStart := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
		users, err := sw.appClient.GetUsersContext(ctx)
		requestLatency := time.Since(requestStart)
		cancel()

		if err != nil {
			glog.Error(errors.Wrap(err, "failed to refresh slack users: "))
			continue
		}

		// Bail early if we have the same number of users, not the most perfect heurstic, but good
		// enough since we also lazily load user names.
		sw.replacerLock.RLock()
		if len(sw.userMap) == len(users) {
			sw.replacerLock.RUnlock()
			continue
		}
		sw.replacerLock.RUnlock()

		// Generate our new map and replacer
		refreshStart := time.Now()
		newMap := make(map[string]string, len(users))
		replacerSlice := make([]string, 0, len(users))
		for _, user := range users {
			newMap[user.ID] = user.Name
			replacerSlice = append(replacerSlice, user.ID, user.Name)
		}
		newReplacer := strings.NewReplacer(replacerSlice...)
		refreshLatency := time.Since(refreshStart)

		// Acquire lock and replace the map and replacer
		sw.replacerLock.Lock()
		sw.userReplacer = newReplacer
		sw.userMap = newMap
		glog.Infof("fresh of %d users complete: downloaded in %v, refreshed in %v", len(newMap), requestLatency, refreshLatency)
		sw.replacerLock.Unlock()
	}
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

	// NOTE(rossdylan) HAX Alert, files are defined with 3 '-' seperate identifiers, in the public
	// URLs the last segment is the public secret used to access the private file link. We have to
	// do it this way since the File structures don't provide a public direct download link
	publicURLParts := strings.Split(slackFile.PermalinkPublic, "-")
	secretBits := "?pub_secret=" + publicURLParts[len(publicURLParts)-1]

	if file.Size > twilioFileSizeLimit {
		largestThumbnail, err := largestThumbnailLink(*file)
		if err != nil {
			return "", errors.Wrapf(err, "failed to share file '%s': ", slackFile.Name)
		}
		return largestThumbnail + secretBits, nil
	}
	return slackFile.URLPrivateDownload + secretBits, nil
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
