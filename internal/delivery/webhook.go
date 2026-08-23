package delivery

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/outbox"
)

type Sender interface {
	Send(context.Context, outbox.Event) error
}

type WebhookSender struct {
	client   *http.Client
	endpoint *url.URL
	apiKey   string
	maxBody  int64
}

func NewWebhookSender(client *http.Client, endpoint, apiKey string) (*WebhookSender, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse webhook endpoint: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("webhook endpoint must use http or https")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &WebhookSender{client: client, endpoint: parsed, apiKey: apiKey, maxBody: 64 << 10}, nil
}

func (s *WebhookSender) Send(ctx context.Context, event outbox.Event) error {
	endpoint := *s.endpoint
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/events/" + url.PathEscape(event.Topic)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(event.Payload))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", event.ID)
	request.Header.Set("X-AridLink-Aggregate", event.AggregateID)
	if s.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("deliver webhook: %w", err)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, s.maxBody))
	if readErr != nil {
		return fmt.Errorf("read webhook response: %w", readErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("webhook returned %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

type LogSender struct{}

func (LogSender) Send(ctx context.Context, _ outbox.Event) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
