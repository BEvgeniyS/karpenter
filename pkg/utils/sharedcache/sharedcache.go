package sharedcache

import (
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
)

var sharedCache *cache.Cache
var once sync.Once

const DefaultSharedCacheTTL time.Duration = time.Hour * 24

// SharedCache provides a global point of access to the Cache instance
func SharedCache() *cache.Cache {
	once.Do(func() {
		sharedCache = cache.New(DefaultSharedCacheTTL, time.Hour)
	})
	return sharedCache
}
