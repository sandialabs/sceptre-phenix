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
	return &GoWebCache{c: gocache.New(gocache.NoExpiration, 30*time.Second)}
}

func (this GoWebCache) Get(key string) ([]byte, bool) {
	v, ok := this.c.Get(key)
	if !ok {
		return nil, false
	}

	return v.([]byte), true
}

func (this *GoWebCache) Set(key string, val []byte) error {
	this.c.Set(key, val, -1)
	return nil
}

func (this *GoWebCache) SetWithExpire(key string, val []byte, exp time.Duration) error {
	this.c.Set(key, val, exp)
	return nil
}

func (this *GoWebCache) Lock(key string, status Status, exp time.Duration) Status {
	key = "LOCK|" + key

	if err := this.c.Add(key, status, exp); err != nil {
		v, ok := this.c.Get(key)

		// This *might* happen if the key expires or is deleted between
		// calling `Add` and `Get`.
		if !ok {
			return ""
		}

		return v.(Status)
	}

	return ""
}

func (this *GoWebCache) Locked(key string) Status {
	key = "LOCK|" + key

	v, ok := this.c.Get(key)
	if !ok {
		return ""
	}

	return v.(Status)
}

func (this *GoWebCache) Unlock(key string) {
	this.c.Delete("LOCK|" + key)
}
