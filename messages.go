package main

import (
	"context"
	"strings"
	"sync"

	"github.com/golang/glog"
	"github.com/hashicorp/golang-lru"
	"github.com/nlopes/slack/slackevents"
	"github.com/pkg/errors"
)

// number of messages to keep in the tracker at once
const messageBacklog = 512

// messageRef combines a channel ID and a timestamp to become a unique pointer to a message
// NOTE(rossdylan): I couldn't really find this explicitly documented but I'm pretty sure this
// is how slack refers to messages
type messageRef struct {
	channel   string
	timestamp string
}

// MessageTracker is a slightly over-complicated way to tracking recent messages. The AppMention
// event doesn't include all the extra metadata like attached files, so we subscribe to
// MessageEvents and cache the ones that mention our bot. I couldn't find any information on
// delivery order semantics so the tracker has an overcomplicated waiting mechanism.
type MessageTracker struct {
	// {channelID, timestamp} -> msg
	// map[messageRef]*slackevents.MessageEvent
	cache *lru.Cache

	waitersLock sync.RWMutex
	waiters     map[messageRef]chan struct{}

	botUID string
}

func NewMessageTracker(botUID string) (*MessageTracker, error) {
	cache, err := lru.New(messageBacklog)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create lru.Cache")
	}

	return &MessageTracker{
		cache:   cache,
		waiters: make(map[messageRef]chan struct{}),
		botUID:  botUID,
	}, nil
}

func (mt *MessageTracker) HandleMessage(ctx context.Context, event *slackevents.MessageEvent) {
	// NOTE(rossdylan): mim is Multiparty Instant Message aka private group chat
	if event.Type == "message" && (event.ChannelType == "channel" || event.ChannelType == "mim") {
		if strings.Contains(event.Text, mt.botUID) {
			ref := messageRef{event.Channel, event.TimeStamp}
			mt.cache.Add(ref, event)
			mt.waitersLock.RLock()
			waiterChan, ok := mt.waiters[ref]
			mt.waitersLock.RUnlock()
			if ok {
				select {
				case waiterChan <- struct{}{}:
					return
				case <-ctx.Done():
					mt.removeWaiter(ref)
				}
			}
		}
	}
}

func (mt *MessageTracker) GetMessage(channel, timestamp string) (*slackevents.MessageEvent, bool) {
	ref := messageRef{channel: channel, timestamp: timestamp}
	event, ok := mt.cache.Get(ref)
	if ok {
		// Remove it since we only need to retrieve it once
		mt.cache.Remove(ref)
	}
	return event.(*slackevents.MessageEvent), ok
}

func (mt *MessageTracker) addWaiter(ref messageRef) chan struct{} {
	waitChan := make(chan struct{})
	mt.waitersLock.Lock()
	mt.waiters[ref] = waitChan
	mt.waitersLock.Unlock()
	glog.Infof("added waiter for %#v", ref)
	return waitChan
}

func (mt *MessageTracker) removeWaiter(ref messageRef) {
	mt.waitersLock.Lock()
	delete(mt.waiters, ref)
	mt.waitersLock.Unlock()
	glog.Infof("removed waiter for %#v", ref)
}

func (mt *MessageTracker) WaitForMessage(ctx context.Context, channel, timestamp string) (*slackevents.MessageEvent, bool) {
	ref := messageRef{channel: channel, timestamp: timestamp}
	if mt.cache.Contains(ref) {
		return mt.GetMessage(channel, timestamp)
	}
	waitChan := mt.addWaiter(ref)
	defer mt.removeWaiter(ref)

	select {
	case <-waitChan:
		glog.Infof("waiter for %#v was informed of message", ref)
		return mt.GetMessage(channel, timestamp)
	case <-ctx.Done():
		return nil, false
	}
}
