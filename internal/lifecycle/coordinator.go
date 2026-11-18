package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrClosing = errors.New("server lifecycle is shutting down")

type ConflictError struct{ Requested, Active string }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s conflicts with active %s; retry after it completes", e.Requested, e.Active)
}

// Coordinator admits one synchronous operation and never queues background work.
type Coordinator struct {
	mu      sync.Mutex
	closing bool
	active  string
	done    chan struct{}
	stopped bool
}

func NewCoordinator() *Coordinator { return &Coordinator{} }

func (c *Coordinator) acquire(name string, shutdown bool) error {
	if c.closing && !shutdown {
		return ErrClosing
	}
	if c.done != nil {
		return &ConflictError{Requested: name, Active: c.active}
	}
	c.active, c.done = name, make(chan struct{})
	return nil
}

func (c *Coordinator) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	close(c.done)
	c.done, c.active = nil, ""
}

func (c *Coordinator) Run(name string, operation func() error) error {
	c.mu.Lock()
	err := c.acquire(name, false)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	defer c.release()
	return operation()
}

func (c *Coordinator) Wait(ctx context.Context, name string, operation func() error) error {
	return c.wait(ctx, name, false, operation)
}

func (c *Coordinator) Shutdown(ctx context.Context, operation func() error) error {
	c.mu.Lock()
	c.closing = true
	c.mu.Unlock()
	return c.wait(ctx, "shutdown", true, operation)
}

func (c *Coordinator) wait(ctx context.Context, name string, shutdown bool, operation func() error) error {
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("waiting for %s: %w", name, err)
		}
		c.mu.Lock()
		if shutdown && c.stopped {
			c.mu.Unlock()
			return ErrClosing
		}
		err := c.acquire(name, shutdown)
		done := c.done
		c.mu.Unlock()
		if err == nil {
			defer c.release()
			if shutdown {
				defer func() { c.mu.Lock(); c.stopped = true; c.mu.Unlock() }()
			}
			return operation()
		}
		if errors.Is(err, ErrClosing) {
			return err
		}
		select {
		case <-done:
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", name, ctx.Err())
		}
	}
}
