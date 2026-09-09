package cachestore

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

func NewClient(address, password string, db int) (*redis.Client, error) {

	newRedisOpts := redis.Options{Addr: address, Password: password, DB: db}
	newRedisClient := redis.NewClient(&newRedisOpts)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := newRedisClient.Ping(ctx).Result(); err != nil {
		_ = newRedisClient.Close()
		return nil, err
	}
	return newRedisClient, nil
}
