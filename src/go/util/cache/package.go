package cache

//nolint:gochecknoglobals // global cache
var DefaultCache Cache = NewGoCache()

func Get(key string) (any, bool) {
	return DefaultCache.Get(key)
}

func Set(key string, val any) error {
	return DefaultCache.Set(key, val)
}
