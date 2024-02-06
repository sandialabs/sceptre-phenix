package cache

import "time"

var DefaultWebCache WebCache = NewGoWebCache()

func Get(key string) ([]byte, bool) {
	return DefaultWebCache.Get(key)
}

func Set(key string, val []byte) error {
	return DefaultWebCache.Set(key, val)
}

func SetWithExpire(key string, val []byte, exp time.Duration) error {
	return DefaultWebCache.SetWithExpire(key, val, exp)
}

func Lock(key string, status Status, exp time.Duration) Status {
	return DefaultWebCache.Lock(key, status, exp)
}

func Locked(key string) Status {
	return DefaultWebCache.Locked(key)
}

func Unlock(key string) {
	DefaultWebCache.Unlock(key)
}
