package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/pkg/errors"
	"golang.org/x/net/context/ctxhttp"
)

const baseURL = "https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json"

type twilioClient struct {
	accountSID  string
	authToken   string
	toNumber    string
	fromNumber  string
	smsEndpoint string

	limiter *Limiter
	client  *http.Client
}

type sendMessageParams struct {
	Message  string
	MediaURL *string
}

func newTwilioClient(config TwilioConfig) (*twilioClient, error) {
	limit, err := time.ParseDuration(config.Limit)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse rate_limit duration '%s': ", config.Limit)
	}

	limiter := NewLimiter(limit)

	return &twilioClient{
		accountSID:  config.SID,
		authToken:   config.Token,
		toNumber:    config.ToNumber,
		fromNumber:  config.FromNumber,
		smsEndpoint: fmt.Sprintf(baseURL, config.SID),
		limiter:     limiter,
		client:      &http.Client{},
	}, nil
}

func (tw *twilioClient) SendMessage(ctx context.Context, params sendMessageParams) error {
	data := url.Values{}
	//TODO(rossdylan): We can add MMS support via the MediaUrl parameter
	data.Set("To", tw.toNumber)
	data.Set("From", tw.fromNumber)
	data.Set("Body", params.Message)
	if params.MediaURL != nil {
		data.Set("MediaUrl", *params.MediaURL)
	}
	req, err := http.NewRequest("POST", tw.smsEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return errors.Wrap(err, "failed to construct request: ")
	}

	req.SetBasicAuth(tw.accountSID, tw.authToken)
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	if acquired := tw.limiter.Acquire(ctx); acquired {
		resp, err := ctxhttp.Do(ctx, tw.client, req)
		if err != nil {
			return errors.Wrap(err, "failed to make twilio request: ")
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			var respMap map[string]interface{}
			decoder := json.NewDecoder(resp.Body)
			err := decoder.Decode(&respMap)
			if err != nil {
				return errors.Wrap(err, "failed to decode response from twilio: ")
			}
			glog.V(2).Infof("twilio response: %#v", respMap)
		} else {
			data, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return errors.Wrap(err, "twilio request failed and failed to read error body: ")
			}
			glog.V(2).Infof("twilio error response: %s", string(data))
		}
	} else {
		glog.V(2).Info("hit the rate limit")
	}

	return nil
}
