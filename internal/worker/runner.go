package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/alert"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/delivery"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/grant"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/outbox"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
)

type Config struct {
	Owner          string
	Interval       time.Duration
	Lease          time.Duration
	BatchSize      int
	MaxAttempts    int
	RetryBase      time.Duration
	HandlerTimeout time.Duration
}

type Runner struct {
	config Config
	clock  platform.Clock
	logger *slog.Logger
	events *outbox.Service
	jobs   *Jobs
	alerts *alert.Service
	grants *grant.Service
	sender delivery.Sender
}

func NewRunner(config Config, clock platform.Clock, logger *slog.Logger, events *outbox.Service, jobs *Jobs,
	alerts *alert.Service, grants *grant.Service, sender delivery.Sender) (*Runner, error) {
	if config.Owner == "" || config.Interval <= 0 || config.Lease <= 0 || config.BatchSize <= 0 || config.MaxAttempts <= 0 || config.HandlerTimeout <= 0 {
		return nil, platform.FieldError{Field: "worker", Message: "complete positive worker configuration required"}
	}
	if logger == nil || events == nil || jobs == nil || alerts == nil || grants == nil || sender == nil {
		return nil, platform.FieldError{Field: "worker", Message: "all worker dependencies are required"}
	}
	return &Runner{config: config, clock: clock, logger: logger, events: events, jobs: jobs, alerts: alerts, grants: grants, sender: sender}, nil
}

func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.config.Interval)
	defer ticker.Stop()
	if err := r.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		r.logger.Error("initial worker cycle failed", "error", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				r.logger.Error("worker cycle failed", "error", err)
			}
		}
	}
}

func (r *Runner) RunOnce(ctx context.Context) error {
	if err := platform.CheckContext(ctx); err != nil {
		return err
	}
	var errs []error
	if _, err := r.alerts.Expire(ctx, r.config.BatchSize); err != nil {
		errs = append(errs, fmt.Errorf("expire alerts: %w", err))
	}
	if _, err := r.grants.ReleaseExpired(ctx, r.config.BatchSize); err != nil {
		errs = append(errs, fmt.Errorf("release reservations: %w", err))
	}
	if err := r.deliverEvents(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := r.processJobs(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (r *Runner) deliverEvents(ctx context.Context) error {
	events, err := r.events.Claim(ctx, r.config.Owner, r.config.Lease, r.config.BatchSize)
	if err != nil {
		return fmt.Errorf("claim outbox events: %w", err)
	}
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 4)
	errorsChannel := make(chan error, len(events))
	for _, event := range events {
		event := event
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				errorsChannel <- ctx.Err()
				return
			}
			deliveryContext, cancel := context.WithTimeout(ctx, r.config.HandlerTimeout)
			err := r.sender.Send(deliveryContext, event)
			cancel()
			if err == nil {
				if ackErr := r.events.Acknowledge(ctx, event.ID, r.config.Owner); ackErr != nil {
					errorsChannel <- fmt.Errorf("ack event %s: %w", event.ID, ackErr)
				}
				return
			}
			retry := retryDelay(r.config.RetryBase, event.Attempts)
			if failErr := r.events.Fail(ctx, event.ID, r.config.Owner, err, retry, r.config.MaxAttempts); failErr != nil {
				errorsChannel <- fmt.Errorf("record event %s failure: %w", event.ID, failErr)
				return
			}
			r.logger.Warn("outbox delivery scheduled for retry", "event_id", event.ID, "topic", event.Topic, "error", err)
		}()
	}
	wg.Wait()
	close(errorsChannel)
	var errs []error
	for err := range errorsChannel {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (r *Runner) processJobs(ctx context.Context) error {
	jobs, err := r.jobs.Claim(ctx, r.config.Owner, r.config.Lease, r.config.BatchSize)
	if err != nil {
		return fmt.Errorf("claim jobs: %w", err)
	}
	for _, job := range jobs {
		if err := platform.CheckContext(ctx); err != nil {
			return err
		}
		handlerContext, cancel := context.WithTimeout(ctx, r.config.HandlerTimeout)
		err := r.handleJob(handlerContext, job)
		cancel()
		if err == nil {
			if err := r.jobs.Succeed(ctx, job.ID, r.config.Owner); err != nil {
				return fmt.Errorf("complete job %s: %w", job.ID, err)
			}
			continue
		}
		if failErr := r.jobs.Fail(ctx, job.ID, r.config.Owner, err, retryDelay(r.config.RetryBase, job.Attempts), r.config.MaxAttempts); failErr != nil {
			return fmt.Errorf("record job %s failure: %w", job.ID, failErr)
		}
	}
	return nil
}

func (r *Runner) handleJob(ctx context.Context, job Job) error {
	switch job.Kind {
	case "alert.expire":
		_, err := r.alerts.Expire(ctx, r.config.BatchSize)
		return err
	case "grant.release_expired":
		_, err := r.grants.ReleaseExpired(ctx, r.config.BatchSize)
		return err
	default:
		return fmt.Errorf("unsupported job kind %q", job.Kind)
	}
}

func retryDelay(base time.Duration, attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 8 {
		attempts = 8
	}
	return base * time.Duration(1<<(attempts-1))
}
