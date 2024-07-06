package sharedcache

import (
	"sync"

	"github.com/patrickmn/go-cache"
)

// private variable to hold the instance
var sharedCache *cache.Cache
var once sync.Once

// SharedCache provides a global point of access to the Cache instance
func SharedCache() *cache.Cache {
	once.Do(func() {
		sharedCache = cache.New(cache.NoExpiration, cache.NoExpiration)
	})
	return sharedCache
}
