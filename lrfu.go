package cachekit

import (
	"sync"
)

// DefaultLRFUCacheSize is the default maximum number of entries when NewLRFUCache is called with maxSize <= 0
const DefaultLRFUCacheSize = 100

type lrfuNode[K comparable, V any] struct {
	key        K
	value      V
	visited    bool
	prev, next *lrfuNode[K, V]
}

// LRFUCache is an in-memory cache that uses the LRFU (Lazy Recency/Frequency Update) eviction algorithm
// It uses lazy promotion (visited bit set on access, tracking frequency) combined with quick demotion
// (new entries are evicted quickly if never accessed again), achieving better hit rates than traditional LRU
// for power-law/Zipf workloads. All operations are O(1). Safe for concurrent use
//
// Eviction: a hand pointer walks from tail towards head. Visited nodes have their bit cleared
// and are skipped; the first unvisited node is evicted. This gives popular entries a second chance
// without the overhead of moving nodes on every access (unlike LRU)
type LRFUCache[K comparable, V any] struct {
	mu      sync.RWMutex
	items   map[K]*lrfuNode[K, V]
	head    *lrfuNode[K, V]
	tail    *lrfuNode[K, V]
	hand    *lrfuNode[K, V]
	maxSize int
}

// NewLRFUCache returns a LRFUCache that holds at most maxSize entries. If maxSize <= 0, DefaultLRFUCacheSize (100) is used
func NewLRFUCache[K comparable, V any](maxSize int) *LRFUCache[K, V] {
	if maxSize <= 0 {
		maxSize = DefaultLRFUCacheSize
	}
	return &LRFUCache[K, V]{
		items:   make(map[K]*lrfuNode[K, V], maxSize),
		maxSize: maxSize,
	}
}

// Get returns the value for key and true if the key exists; otherwise the zero value of V and false
// Marks the entry as visited (lazy promotion) so it survives the next eviction pass
// Nil receiver is safe and returns (zero, false)
func (c *LRFUCache[K, V]) Get(key K) (V, bool) {
	if c == nil {
		var zero V
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	n.visited = true
	return n.value, true
}

// Peek returns the value for key without marking it as visited. Does not affect eviction order
// Nil receiver is safe and returns (zero, false)
func (c *LRFUCache[K, V]) Peek(key K) (V, bool) {
	if c == nil {
		var zero V
		return zero, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	n, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	return n.value, true
}

func (c *LRFUCache[K, V]) addToHead(n *lrfuNode[K, V]) {
	n.prev = nil
	n.next = c.head
	if c.head != nil {
		c.head.prev = n
	}
	c.head = n
	if c.tail == nil {
		c.tail = n
	}
}

func (c *LRFUCache[K, V]) removeNode(n *lrfuNode[K, V]) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		c.head = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		c.tail = n.prev
	}
	n.prev = nil
	n.next = nil
}

func (c *LRFUCache[K, V]) evict() {
	h := c.hand
	if h == nil {
		h = c.tail
	}
	for h != nil && h.visited {
		h.visited = false
		h = h.prev
		if h == nil {
			h = c.tail
		}
	}
	if h == nil {
		return
	}
	c.hand = h.prev
	if c.hand == nil {
		c.hand = c.tail
	}
	c.removeNode(h)
	delete(c.items, h.key)
	// Fix hand if it pointed to the evicted node (single-element edge case)
	if c.hand == h {
		c.hand = nil
	}
}

// Set inserts or updates the entry for key. If the key already exists, its value is updated
// and the entry is marked as visited. If the cache is full, the LRFU algorithm gracefully removes
// the least valuable entry. No-op if the receiver is nil
func (c *LRFUCache[K, V]) Set(key K, value V) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if n, ok := c.items[key]; ok {
		n.value = value
		n.visited = true
		return
	}
	if len(c.items) >= c.maxSize {
		c.evict()
	}
	n := &lrfuNode[K, V]{key: key, value: value}
	c.items[key] = n
	c.addToHead(n)
	if c.hand == nil {
		c.hand = c.tail
	}
}

// SetIfAbsent adds the entry for key only if it does not exist; does nothing if the key is already present. No-op if the receiver is nil
func (c *LRFUCache[K, V]) SetIfAbsent(key K, value V) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; ok {
		return
	}
	if len(c.items) >= c.maxSize {
		c.evict()
	}
	n := &lrfuNode[K, V]{key: key, value: value}
	c.items[key] = n
	c.addToHead(n)
	if c.hand == nil {
		c.hand = c.tail
	}
}

// Delete removes the entry for key if present. Adjusts the hand pointer if it pointed to the deleted node. No-op if the key is not in the cache or the receiver is nil
func (c *LRFUCache[K, V]) Delete(key K) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n, ok := c.items[key]
	if !ok {
		return
	}
	if c.hand == n {
		c.hand = n.prev
		if c.hand == nil {
			c.hand = c.tail
		}
	}
	c.removeNode(n)
	delete(c.items, key)
	if len(c.items) == 0 {
		c.hand = nil
	}
}

// Len returns the number of entries in the cache. Returns 0 for a nil receiver
func (c *LRFUCache[K, V]) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Cap returns the maximum number of entries the cache can hold (maxSize). Returns 0 for a nil receiver
func (c *LRFUCache[K, V]) Cap() int {
	if c == nil {
		return 0
	}
	return c.maxSize
}

// Flush removes all entries from the cache and resets internal state. No-op if the receiver is nil
func (c *LRFUCache[K, V]) Flush() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.items)
	c.head = nil
	c.tail = nil
	c.hand = nil
}
