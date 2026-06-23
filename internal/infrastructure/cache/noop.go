package cache

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("cache: key not found")

// NoopClient is a stub used until the real Redis client is wired in TD-03.
type NoopClient struct{}

func (n *NoopClient) Get(_ context.Context, _ string) (string, error) {
	return "", ErrNotFound
}

func (n *NoopClient) Set(_ context.Context, _, _ string, _ time.Duration) error {
	return nil
}

func (n *NoopClient) SetNX(_ context.Context, _, _ string, _ time.Duration) (bool, error) {
	return true, nil
}

func (n *NoopClient) Delete(_ context.Context, _ string) error {
	return nil
}
