package cachekit

import (
	"container/list"
	"math/rand"
	"testing"
)

const (
	workloadCacheCap = 1024
	workloadTraceLen = 1 << 16
)

type workloadCache interface {
	Get(int) (int, bool)
	Set(int, int)
}

type lruBenchEntry struct {
	key   int
	value int
}

type lruBenchCache struct {
	capacity int
	order    *list.List
	items    map[int]*list.Element
}

func newLRUBenchCache(capacity int) *lruBenchCache {
	return &lruBenchCache{
		capacity: capacity,
		order:    list.New(),
		items:    make(map[int]*list.Element, capacity),
	}
}

func (c *lruBenchCache) Get(key int) (int, bool) {
	if elem, ok := c.items[key]; ok {
		c.order.MoveToFront(elem)
		return elem.Value.(lruBenchEntry).value, true //nolint:forcetypeassert // benchmark cache stores only lruBenchEntry values
	}
	return 0, false
}

func (c *lruBenchCache) Set(key, value int) {
	if elem, ok := c.items[key]; ok {
		elem.Value = lruBenchEntry{key: key, value: value}
		c.order.MoveToFront(elem)
		return
	}
	if len(c.items) >= c.capacity {
		victim := c.order.Back()
		if victim != nil {
			entry := victim.Value.(lruBenchEntry) //nolint:forcetypeassert,revive // benchmark cache stores only lruBenchEntry values
			delete(c.items, entry.key)
			c.order.Remove(victim)
		}
	}
	c.items[key] = c.order.PushFront(lruBenchEntry{key: key, value: value})
}

func runWorkloadBenchmark(b *testing.B, keys []int, cache workloadCache) {
	b.Helper()
	b.ReportAllocs()
	var hits int
	mask := len(keys) - 1
	b.ResetTimer()
	for i := range b.N {
		key := keys[i&mask]
		if _, ok := cache.Get(key); ok {
			hits++
			continue
		}
		cache.Set(key, key)
	}
	b.StopTimer()
	if b.N > 0 {
		b.ReportMetric(float64(hits)*100/float64(b.N), "hit_pct")
	}
}

func zipfTrace() []int {
	r := rand.New(rand.NewSource(1)) //nolint:gosec // deterministic benchmark trace
	z := rand.NewZipf(r, 1.2, 1, 8191)
	keys := make([]int, workloadTraceLen)
	for i := range keys {
		keys[i] = int(z.Uint64())
	}
	return keys
}

func scanTrace() []int {
	keys := make([]int, workloadTraceLen)
	for i := range keys {
		keys[i] = i & 4095
	}
	return keys
}

func mixedHotScanTrace() []int {
	keys := make([]int, workloadTraceLen)
	cold := 0
	for i := range keys {
		if i%4 == 0 {
			keys[i] = 1024 + (cold & 8191)
			cold++
			continue
		}
		keys[i] = i & 511
	}
	return keys
}

func BenchmarkSieveWorkload_Zipf(b *testing.B) {
	runWorkloadBenchmark(b, zipfTrace(), NewSieveCache[int, int](workloadCacheCap))
}

func BenchmarkLRUWorkload_Zipf(b *testing.B) {
	runWorkloadBenchmark(b, zipfTrace(), newLRUBenchCache(workloadCacheCap))
}

func BenchmarkSieveWorkload_Scan(b *testing.B) {
	runWorkloadBenchmark(b, scanTrace(), NewSieveCache[int, int](workloadCacheCap))
}

func BenchmarkLRUWorkload_Scan(b *testing.B) {
	runWorkloadBenchmark(b, scanTrace(), newLRUBenchCache(workloadCacheCap))
}

func BenchmarkSieveWorkload_MixedHotScan(b *testing.B) {
	runWorkloadBenchmark(b, mixedHotScanTrace(), NewSieveCache[int, int](workloadCacheCap))
}

func BenchmarkLRUWorkload_MixedHotScan(b *testing.B) {
	runWorkloadBenchmark(b, mixedHotScanTrace(), newLRUBenchCache(workloadCacheCap))
}
