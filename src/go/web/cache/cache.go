package cache

import (
	"time"

	gocache "github.com/patrickmn/go-cache"
)

type Status string

const (
	StatusStopping     Status = "stopping"
	StatusStopped      Status = "stopped"
	StatusStarting     Status = "starting"
	StatusStarted      Status = "started"
	StatusCreating     Status = "creating"
	StatusUpdating     Status = "updating"
	StatusDeleting     Status = "deleting"
	StatusRedeploying  Status = "redeploying"
	StatusSnapshotting Status = "snapshotting"
	StatusRestoring    Status = "restoring"
	StatusCommitting   Status = "committing"

	defaultCleanupInterval = 30 * time.Second
)

type WebCache interface {
	Get(string) ([]byte, bool)
	Set(string, []byte) error
	SetWithExpire(string, []byte, time.Duration) error

	Lock(string, Status, time.Duration) Status
	Locked(string) Status
	Unlock(string)
}

type GoWebCache struct {
	c *gocache.Cache
}

func NewGoWebCache() *GoWebCache {
	return &GoWebCache{c: gocache.New(gocache.NoExpiration, defaultCleanupInterval)}
}

func (gwc GoWebCache) Get(key string) ([]byte, bool) {
	v, ok := gwc.c.Get(key)
	if !ok {
		return nil, false
	}

	val, ok := v.([]byte)
	if !ok {
		return nil, false
	}
	return val, true
}

func (gwc *GoWebCache) Set(key string, val []byte) error {
	gwc.c.Set(key, val, -1)

	return nil
}

func (gwc *GoWebCache) SetWithExpire(key string, val []byte, exp time.Duration) error {
	gwc.c.Set(key, val, exp)

	return nil
}

func (gwc *GoWebCache) Lock(key string, status Status, exp time.Duration) Status {
	key = "LOCK|" + key

	err := gwc.c.Add(key, status, exp)
	if err != nil {
		v, ok := gwc.c.Get(key)

		// This *might* happen if the key expires or is deleted between
		// calling `Add` and `Get`.
		if !ok {
			return ""
		}

		val, ok := v.(Status)
		if !ok {
			return ""
		}
		return val
	}

	return ""
}

func (gwc *GoWebCache) Locked(key string) Status {
	key = "LOCK|" + key

	v, ok := gwc.c.Get(key)
	if !ok {
		return ""
	}

	val, ok := v.(Status)
	if !ok {
		return ""
	}
	return val
}

func (gwc *GoWebCache) Unlock(key string) {
	gwc.c.Delete("LOCK|" + key)
}
