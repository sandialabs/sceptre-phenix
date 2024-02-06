package cache

import (
	"time"
)

type Cache interface {
	Get(string) (any, bool)
	Set(string, any) error
	SetWithExpire(string, any, time.Duration) error
}
