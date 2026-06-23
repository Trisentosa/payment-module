package redis

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

type Adapter struct {
	client *redis.Client
}

func NewAdapter(addr, password string) (*Adapter, error) {
	c := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
	})
	if err := c.Ping(context.Background()).Err(); err != nil {
		return nil, apperror.Internal("redis ping failed", err)
	}
	return &Adapter{client: c}, nil
}

func (a *Adapter) Get(ctx context.Context, key string) (string, error) {
	val, err := a.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", apperror.NotFound("cache miss: " + key)
	}
	if err != nil {
		return "", apperror.Internal("redis get", err)
	}
	return val, nil
}

func (a *Adapter) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if err := a.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return apperror.Internal("redis set", err)
	}
	return nil
}

func (a *Adapter) SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	ok, err := a.client.SetNX(ctx, key, value, ttl).Result()
	if err != nil {
		return false, apperror.Internal("redis setnx", err)
	}
	return ok, nil
}

func (a *Adapter) Delete(ctx context.Context, key string) error {
	if err := a.client.Del(ctx, key).Err(); err != nil {
		return apperror.Internal("redis del", err)
	}
	return nil
}

func (a *Adapter) Close() error {
	return a.client.Close()
}
