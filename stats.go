package main

import (
	"fmt"
	"sync/atomic"

	"github.com/go-logr/logr"
)

type CacheStats struct {
	// hits, misses and errors are mutually exclusive
	// sum must equal total requests
	hits   int64
	misses int64
	errors int64
}

func (c *CacheStats) Hits() int64 {
	return atomic.LoadInt64(&c.hits)
}

func (c *CacheStats) Hit() {
	atomic.AddInt64(&c.hits, 1)
}

func (c *CacheStats) Misses() int64 {
	return atomic.LoadInt64(&c.misses)
}

func (c *CacheStats) Miss() {
	atomic.AddInt64(&c.misses, 1)
}

func (c *CacheStats) Errors() int64 {
	return atomic.LoadInt64(&c.errors)
}

func (c *CacheStats) Error() {
	atomic.AddInt64(&c.errors, 1)
}

func (c *CacheStats) Log(cache string, logger logr.Logger) {
	if !logger.Enabled() {
		return
	}

	hits := atomic.LoadInt64(&c.hits)
	misses := atomic.LoadInt64(&c.misses)
	errors := atomic.LoadInt64(&c.errors)

	requests := hits + misses + errors

	hitRate := fmt.Sprintf("%0.2f", float64(requests-misses-errors)/float64(requests))
	errorRate := fmt.Sprintf("%0.2f", float64(errors)/float64(requests))

	logger.Info("Cache stats", "cache", cache, "requests", requests, "hits", hits, "misses", misses, "errors", errors, "hit_rate", hitRate, "error_rate", errorRate)
}
