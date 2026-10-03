package eventbus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Source is where a Consumer pulls events from.
type Source interface {
	Fetch(ctx context.Context, limit int) ([]Message, error)
	Ack(ctx context.Context, upTo int64) error
}

// Handler applies one event. It must be idempotent (de-duplicate on EventID).
// Return Permanent(err) when retrying cannot help.
type Handler func(ctx context.Context, m Message) error

// DeadLetterFunc records an event that will not be applied, so an operator can
// inspect and replay it. If it fails, the event is retried later instead.
type DeadLetterFunc func(ctx context.Context, m Message, cause error) error

// ConsumerConfig configures a Consumer. Source, Handle and DeadLetter are required.
type ConsumerConfig struct {
	Name       string // for logs, e.g. "payments-events"
	Source     Source
	Handle     Handler
	DeadLetter DeadLetterFunc
	Log        *slog.Logger
	// Interval is the idle poll interval (default 1s).
	Interval time.Duration
	// Batch is the pull size (default 100, max MaxBatch).
	Batch int
	// MaxBackoff caps the retry delay after failures (default 1m).
	MaxBackoff time.Duration
	// PoisonAfter is how many consecutive failures of one event with a
	// retryable error dead-letter it (default 20, roughly 15 minutes of
	// backoff), so one bad event cannot stall every later event forever.
	PoisonAfter int
}

// Consumer pulls events in order, applies them, and acknowledges progress.
type Consumer struct {
	cfg      ConsumerConfig
	log      *slog.Logger
	attempts map[string]int
}

// NewConsumer validates cfg and applies defaults.
func NewConsumer(cfg ConsumerConfig) (*Consumer, error) {
	if cfg.Source == nil || cfg.Handle == nil || cfg.DeadLetter == nil {
		return nil, errors.New("eventbus: consumer needs Source, Handle and DeadLetter")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	if cfg.Batch <= 0 || cfg.Batch > MaxBatch {
		cfg.Batch = 100
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = time.Minute
	}
	if cfg.PoisonAfter <= 0 {
		cfg.PoisonAfter = 20
	}
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Consumer{cfg: cfg, log: log.With("consumer", cfg.Name), attempts: map[string]int{}}, nil
}

// Run consumes until ctx is done, then returns nil. Failures are logged and
// retried with exponential backoff; they never end the loop.
func (c *Consumer) Run(ctx context.Context) error {
	backoff := time.Duration(0)
	for {
		n, err := c.Step(ctx)
		if ctx.Err() != nil {
			return nil
		}
		wait := c.cfg.Interval
		switch {
		case err != nil:
			if backoff == 0 {
				backoff = c.cfg.Interval
			} else {
				backoff = min(2*backoff, c.cfg.MaxBackoff)
			}
			wait = backoff
			c.log.Warn("event consumer step failed; backing off", "err", err, "retry_in", backoff)
		case n == c.cfg.Batch:
			backoff, wait = 0, 0 // more is waiting: keep going
		default:
			backoff = 0
		}
		if wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil
			case <-t.C:
			}
		}
	}
}

// Step pulls one batch, applies events in order until one fails, and acks
// everything before the failure. It returns how many events were completed.
func (c *Consumer) Step(ctx context.Context) (int, error) {
	msgs, err := c.cfg.Source.Fetch(ctx, c.cfg.Batch)
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	var (
		done    int64
		handled int
		stepErr error
	)
	for _, m := range msgs {
		if err := c.apply(ctx, m); err != nil {
			stepErr = err
			break
		}
		done, handled = m.Seq, handled+1
	}
	if done > 0 {
		if err := c.cfg.Source.Ack(ctx, done); err != nil {
			return handled, errors.Join(stepErr, fmt.Errorf("ack: %w", err))
		}
	}
	return handled, stepErr
}

// apply runs the handler; a nil return means the event is done (applied or
// dead-lettered) and may be acknowledged.
func (c *Consumer) apply(ctx context.Context, m Message) error {
	err := c.cfg.Handle(ctx, m)
	if err == nil {
		delete(c.attempts, m.EventID)
		return nil
	}
	if ctx.Err() != nil {
		return err
	}
	c.attempts[m.EventID]++
	n := c.attempts[m.EventID]
	if !IsPermanent(err) && n < c.cfg.PoisonAfter {
		c.log.Warn("event failed; will retry", "event_id", m.EventID, "topic", m.Topic, "attempt", n, "err", err)
		return fmt.Errorf("event %s (%s): %w", m.EventID, m.Topic, err)
	}
	if dlErr := c.cfg.DeadLetter(ctx, m, err); dlErr != nil {
		return fmt.Errorf("dead-letter event %s: %w", m.EventID, dlErr)
	}
	delete(c.attempts, m.EventID)
	c.log.Error("event dead-lettered", "event_id", m.EventID, "topic", m.Topic, "attempts", n, "err", err)
	return nil
}
