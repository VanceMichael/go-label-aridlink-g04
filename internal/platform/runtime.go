package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type FixedClock struct {
	mu  sync.RWMutex
	now time.Time
}

func NewFixedClock(now time.Time) *FixedClock {
	return &FixedClock{now: now.UTC()}
}

func (c *FixedClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *FixedClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type IDGenerator interface {
	New(prefix string) string
}

type RandomIDs struct{}

func (RandomIDs) New(prefix string) string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("generate id: %v", err))
	}
	return prefix + "_" + hex.EncodeToString(raw[:])
}

type SequenceIDs struct {
	mu   sync.Mutex
	next int
}

func (s *SequenceIDs) New(prefix string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	return fmt.Sprintf("%s_%06d", prefix, s.next)
}

func CheckContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
