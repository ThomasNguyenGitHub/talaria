// Copyright 2019-2020 Grabtaxi Holdings PTE LTE (GRAB), All rights reserved.
// Use of this source code is governed by an MIT-style license that can be found in the LICENSE file

package s3sqs

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"time"

	awssqs "github.com/aws/aws-sdk-go/service/sqs"
	"github.com/grab/talaria/internal/config"
	"github.com/grab/talaria/internal/ingress/s3sqs/s3"
	"github.com/grab/talaria/internal/ingress/s3sqs/sqs"
	"github.com/grab/talaria/internal/monitor"
	"github.com/grab/talaria/internal/monitor/errors"
	"golang.org/x/sync/semaphore"
)

const (
	concurrency = 10
	ctxTag      = "s3sqs"
)

// Ingress represents an ingress layer.
type Ingress struct {
	sqs     Reader              // The SQS reader to use.
	s3      Downloader          // The S3 downloader to use.
	monitor monitor.Monitor     // The monitor to use.
	cancel  context.CancelFunc  // The cancellation function to apply at the end.
	limit   *semaphore.Weighted // The limit of workers
}

// Downloader represents an object downloader
type Downloader interface {
	Download(ctx context.Context, bucket, key string) ([]byte, error)
}

// Reader represents a consumer for SQS
type Reader interface {
	io.Closer
	StartPolling(maxPerRead, sleepMs int64, attributeNames, messageAttributeNames []*string) <-chan *awssqs.Message
	DeleteMessage(msg *awssqs.Message) error
}

// New creates a new ingestion with SQS/S3 files.
func New(conf *config.S3SQS, region string, monitor monitor.Monitor) (*Ingress, error) {
	downloader := s3.New(region, 5, monitor)
	reader, err := sqs.NewReader(conf, region)
	if err != nil {
		return nil, err
	}

	return NewWith(reader, downloader, monitor), nil
}

// NewWith creates a new ingestion with SQS/S3 files.
func NewWith(reader Reader, downloader Downloader, monitor monitor.Monitor) *Ingress {
	return &Ingress{
		sqs:     reader,
		s3:      downloader,
		monitor: monitor,
		limit:   semaphore.NewWeighted(concurrency),
	}
}

// Range iterates through the queue, stops only if Close() is called or the f callback
// returns true.
func (s *Ingress) Range(f func(v []byte) bool) {

	// Create a cancellation context
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	// Start draining the queue, asynchronously
	queue := s.sqs.StartPolling(1, 100, nil, nil)
	go s.drain(ctx, queue, f)
}

// drains files from SQS
func (s *Ingress) drain(ctx context.Context, queue <-chan *awssqs.Message, handler func(v []byte) bool) {
	const tag = "drain"
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-queue:
			if msg == nil || msg.Body == nil {
				continue
			}

			// Ack message received
			if err := s.acknowledge(msg); err != nil {
				s.monitor.Error(err)
				continue
			}

			// Unmarshal the event
			var events events
			if err := json.Unmarshal([]byte(*msg.Body), &events); err != nil {
				s.monitor.Error(errors.Internal("sqs: unable to unmarshal", err))
				continue // Ignore corrupt events
			}

			for _, event := range events.Records {
				bucket := event.S3.Bucket.Name
				key, err := url.QueryUnescape(event.S3.Object.Key)
				if err != nil {
					s.monitor.Error(errors.Internal("sqs: unable to unescape query", err))
					continue
				}

				// Wait until we can proceed
				if err := s.limit.Acquire(ctx, 1); err != nil {
					s.monitor.Debug("sqs: reached concurrency limit")
					continue
				}

				go s.ingest(bucket, key, handler)
			}
		}
	}
}

// Acknowledge deletes the message from SQS
func (s *Ingress) acknowledge(msg *awssqs.Message) error {
	if msg.ReceiptHandle == nil {
		return nil
	}

	if err := s.sqs.DeleteMessage(msg); err != nil {
		return errors.Internal("sqs: unable to delete", err)
	}
	return nil
}

// Ingest downloads an object from S3 and applies a handler to the downloaded
// payload. Few of these can be executed in parallel.
func (s *Ingress) ingest(bucket, key string, handler func(v []byte) bool) {
	defer s.monitor.Duration(ctxTag, "s3sqs", time.Now())

	data, err := s.s3.Download(context.Background(), bucket, key)
	defer s.limit.Release(1)
	if err != nil {
		s.monitor.Error(err)
		return
	}

	s.monitor.Info("sqs: downloading %v", key)

	// Call the handler
	_ = handler(data)
}

// Close stops consuming
func (s *Ingress) Close() {
	s.cancel()
	s.sqs.Close()

	// Wait for ingestion to finish ...
	_ = s.limit.Acquire(context.Background(), concurrency)
	return
}

type events struct {
	Records []struct {
		EventVersion string    `json:"eventVersion"`
		EventSource  string    `json:"eventSource"`
		AwsRegion    string    `json:"awsRegion"`
		EventTime    time.Time `json:"eventTime"`
		EventName    string    `json:"eventName"`
		UserIdentity struct {
			PrincipalID string `json:"principalId"`
		} `json:"userIdentity"`
		RequestParameters struct {
			SourceIPAddress string `json:"sourceIPAddress"`
		} `json:"requestParameters"`
		ResponseElements struct {
			XAmzRequestID string `json:"x-amz-request-id"`
			XAmzID2       string `json:"x-amz-id-2"`
		} `json:"responseElements"`
		S3 struct {
			S3SchemaVersion string `json:"s3SchemaVersion"`
			ConfigurationID string `json:"configurationId"`
			Bucket          struct {
				Name          string `json:"name"`
				OwnerIdentity struct {
					PrincipalID string `json:"principalId"`
				} `json:"ownerIdentity"`
				Arn string `json:"arn"`
			} `json:"bucket"`
			Object struct {
				Key       string `json:"key"`
				Size      int    `json:"size"`
				ETag      string `json:"eTag"`
				Sequencer string `json:"sequencer"`
			} `json:"object"`
		} `json:"s3"`
	} `json:"Records"`
}
