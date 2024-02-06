package cache

import "time"

var DefaultCache Cache = NewGoCache()

func Get(key string) (any, bool) {
	return DefaultCache.Get(key)
}

func Set(key string, val any) error {
	return DefaultCache.Set(key, val)
}

func SetWithExpire(key string, val any, exp time.Duration) error {
	return DefaultCache.SetWithExpire(key, val, exp)
}
