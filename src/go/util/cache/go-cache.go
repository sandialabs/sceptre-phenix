package cache

import (
	"time"

	gocache "github.com/patrickmn/go-cache"
)

type GoCache struct {
	c *gocache.Cache
}

func NewGoCache() *GoCache {
	return &GoCache{c: gocache.New(gocache.NoExpiration, 30*time.Second)}
}

func (this GoCache) Get(key string) (any, bool) {
	v, ok := this.c.Get(key)
	if !ok {
		return nil, false
	}

	return v, true
}

func (this *GoCache) Set(key string, val any) error {
	this.c.Set(key, val, -1)
	return nil
}

func (this *GoCache) SetWithExpire(key string, val any, exp time.Duration) error {
	this.c.Set(key, val, exp)
	return nil
}
