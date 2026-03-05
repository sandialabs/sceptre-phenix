package cache

import (
	"time"

	gocache "github.com/patrickmn/go-cache"
)

const defaultCleanupInterval = 30 * time.Second

type GoCache struct {
	c *gocache.Cache
}

func NewGoCache() *GoCache {
	return &GoCache{c: gocache.New(gocache.NoExpiration, defaultCleanupInterval)}
}

func (c GoCache) Get(key string) (any, bool) {
	v, ok := c.c.Get(key)
	if !ok {
		return nil, false
	}

	return v, true
}

func (c *GoCache) Set(key string, val any) error {
	c.c.Set(key, val, -1)

	return nil
}

func (c *GoCache) SetWithExpire(key string, val any, exp time.Duration) error {
	c.c.Set(key, val, exp)

	return nil
}
