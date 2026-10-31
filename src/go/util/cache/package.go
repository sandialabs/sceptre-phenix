package cache

import "time"

//nolint:gochecknoglobals // global cache
var DefaultCache Cache = NewGoCache()

func Get(key string) (any, bool) {
	return DefaultCache.Get(key)
}

func SetWithExpire(key string, val any, exp time.Duration) error {
	return DefaultCache.SetWithExpire(key, val, exp)
}
