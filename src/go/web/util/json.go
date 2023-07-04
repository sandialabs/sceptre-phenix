package util

func WithRoot(key string, obj any) map[string]any {
	return map[string]any{key: obj}
}
