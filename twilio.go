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

type TwilioClient struct {
	accountSID  string
	authToken   string
	toNumber    string
	fromNumber  string
	smsEndpoint string

	limiter *Limiter
	client  *http.Client
}

type SendMessageParams struct {
	Message  string
	MediaURL *string
	Chunked  bool
}

func NewTwilioClient(config TwilioConfig) (*TwilioClient, error) {
	limit, err := time.ParseDuration(config.Limit)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse rate_limit duration '%s': ", config.Limit)
	}

	limiter := NewLimiter(limit)

	return &TwilioClient{
		accountSID:  config.SID,
		authToken:   config.Token,
		toNumber:    config.ToNumber,
		fromNumber:  config.FromNumber,
		smsEndpoint: fmt.Sprintf(baseURL, config.SID),
		limiter:     limiter,
		client:      &http.Client{},
	}, nil
}

func (tw *TwilioClient) SendMessage(ctx context.Context, params SendMessageParams) error {
	data := url.Values{}
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
	if acquired := tw.limiter.Acquire(ctx); params.Chunked || acquired {
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
			glog.V(2).Infof(
				"message queued from: %s size: %d mms: %t segments: %s status: %v",
				strings.Split(params.Message, ":")[0],
				len(params.Message),
				params.MediaURL != nil,
				respMap["num_segments"],
				respMap["status"],
			)
		} else {
			data, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				return errors.Wrap(err, "twilio request failed and failed to read error body: ")
			}
			return errors.New(fmt.Sprintf("twilio error received: %s", string(data)))
		}
	} else {
		return errors.New("rate limit hit")
	}

	return nil
}
