package ports

import "context"

type CachePort interface {
	Delete(ctx context.Context, key string) error
}
