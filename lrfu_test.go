package cachekit

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLRFUCache_GetSet(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](10)
	_, ok := c.Get("a")
	assert.False(t, ok)
	c.Set("a", 1)
	v, ok := c.Get("a")
	require.True(t, ok)
	assert.Equal(t, 1, v)
	c.Set("b", 2)
	v, _ = c.Get("b")
	assert.Equal(t, 2, v)
	assert.Equal(t, 2, c.Len())
}

func TestLRFUCache_Eviction_UnvisitedFirst(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](3)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	// Access a and c (mark visited), leave b unvisited
	c.Get("a")
	c.Get("c")
	// Insert d - should evict b (unvisited)
	c.Set("d", 4)
	assert.Equal(t, 3, c.Len())
	_, ok := c.Get("b")
	assert.False(t, ok, "b should be evicted (unvisited)")
	v, ok := c.Get("a")
	require.True(t, ok)
	assert.Equal(t, 1, v)
	v, ok = c.Get("c")
	require.True(t, ok)
	assert.Equal(t, 3, v)
	v, ok = c.Get("d")
	require.True(t, ok)
	assert.Equal(t, 4, v)
}

func TestLRFUCache_VisitedSurvivesEviction(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](2)
	c.Set("a", 1)
	c.Set("b", 2)
	// Visit both
	c.Get("a")
	c.Get("b")
	// Insert c - all visited, LRFU clears bits and evicts one
	c.Set("c", 3)
	assert.Equal(t, 2, c.Len())
	// c must be present
	v, ok := c.Get("c")
	require.True(t, ok)
	assert.Equal(t, 3, v)
}

func TestLRFUCache_AllVisited_EvictsAfterClearing(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](3)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	// Visit all
	c.Get("a")
	c.Get("b")
	c.Get("c")
	// Insert d - hand wraps around clearing bits then evicts
	c.Set("d", 4)
	assert.Equal(t, 3, c.Len())
	v, ok := c.Get("d")
	require.True(t, ok)
	assert.Equal(t, 4, v)
	// One of a/b/c is evicted; total must be 3
	present := 0
	for _, k := range []string{"a", "b", "c"} {
		if _, ok := c.Peek(k); ok {
			present++
		}
	}
	assert.Equal(t, 2, present, "exactly one of a/b/c should be evicted")
}

func TestLRFUCache_SetOverwrites(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](5)
	c.Set("a", 1)
	c.Set("a", 2)
	assert.Equal(t, 1, c.Len())
	v, _ := c.Get("a")
	assert.Equal(t, 2, v)
}

func TestLRFUCache_SetIfAbsent_NoOverwrite(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](5)
	c.SetIfAbsent("a", 1)
	c.SetIfAbsent("a", 2)
	assert.Equal(t, 1, c.Len())
	v, _ := c.Get("a")
	assert.Equal(t, 1, v)
}

func TestLRFUCache_Delete(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](5)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	c.Delete("b")
	assert.Equal(t, 2, c.Len())
	_, ok := c.Get("b")
	assert.False(t, ok)
	va, oka := c.Get("a")
	require.True(t, oka)
	assert.Equal(t, 1, va)
	vc, okc := c.Get("c")
	require.True(t, okc)
	assert.Equal(t, 3, vc)
}

func TestLRFUCache_Delete_Head(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](3)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	// Head is c (last inserted)
	c.Delete("c")
	assert.Equal(t, 2, c.Len())
	c.Set("d", 4)
	assert.Equal(t, 3, c.Len())
	vd, okd := c.Get("d")
	require.True(t, okd)
	assert.Equal(t, 4, vd)
}

func TestLRFUCache_Delete_Tail(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](3)
	c.Set("a", 1) // tail
	c.Set("b", 2)
	c.Set("c", 3) // head
	c.Delete("a")
	assert.Equal(t, 2, c.Len())
	c.Set("d", 4)
	assert.Equal(t, 3, c.Len())
}

func TestLRFUCache_Delete_SingleElement(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](3)
	c.Set("a", 1)
	c.Delete("a")
	assert.Equal(t, 0, c.Len())
	c.Set("b", 2)
	v, ok := c.Get("b")
	require.True(t, ok)
	assert.Equal(t, 2, v)
}

func TestLRFUCache_Peek(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](3)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	// Peek a - should NOT mark visited
	v, ok := c.Peek("a")
	require.True(t, ok)
	assert.Equal(t, 1, v)
	// Visit b and c
	c.Get("b")
	c.Get("c")
	// Insert d - a should be evicted (unvisited, Peek doesn't visit)
	c.Set("d", 4)
	_, ok = c.Get("a")
	assert.False(t, ok, "a should be evicted because Peek does not mark visited")
}

func TestLRFUCache_Flush(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](10)
	for i := range 10 {
		c.Set(fmt.Sprintf("k%d", i), i)
	}
	assert.Equal(t, 10, c.Len())
	c.Flush()
	assert.Equal(t, 0, c.Len())
	c.Set("new", 42)
	v, ok := c.Get("new")
	require.True(t, ok)
	assert.Equal(t, 42, v)
}

func TestLRFUCache_DefaultSize(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](0)
	require.NotNil(t, c)
	assert.Equal(t, DefaultLRFUCacheSize, c.Cap())
	for i := range DefaultLRFUCacheSize {
		c.Set(fmt.Sprintf("k%d", i), i)
	}
	assert.Equal(t, DefaultLRFUCacheSize, c.Len())
}

func TestLRFUCache_NilSafe(t *testing.T) {
	t.Parallel()
	var c *LRFUCache[string, int]
	_, ok := c.Get("a")
	assert.False(t, ok)
	_, ok = c.Peek("a")
	assert.False(t, ok)
	c.Set("a", 1)
	c.SetIfAbsent("a", 1)
	c.Delete("a")
	c.Flush()
	assert.Equal(t, 0, c.Len())
	assert.Equal(t, 0, c.Cap())
}

func TestLRFUCache_Concurrent(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[int, int](100)
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.Set(n, n*2)
			c.Get(n)
			c.Peek(n)
			if n%3 == 0 {
				c.Delete(n)
			}
		}(i)
	}
	wg.Wait()
	assert.LessOrEqual(t, c.Len(), 100)
}

func TestLRFUCache_Cap(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](42)
	assert.Equal(t, 42, c.Cap())
	var nilCache *LRFUCache[string, int]
	assert.Equal(t, 0, nilCache.Cap())
}

func TestLRFUCache_EvictionOrder_RespectsVisited(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](4)
	c.Set("a", 1) // insert order: a, b, c, d
	c.Set("b", 2)
	c.Set("c", 3)
	c.Set("d", 4)
	// Visit a, b - leave c, d unvisited
	c.Get("a")
	c.Get("b")
	// Insert e - should evict one of the unvisited (c or d)
	c.Set("e", 5)
	assert.Equal(t, 4, c.Len())
	_, okA := c.Peek("a")
	_, okB := c.Peek("b")
	_, okE := c.Peek("e")
	assert.True(t, okA, "a was visited, must survive")
	assert.True(t, okB, "b was visited, must survive")
	assert.True(t, okE, "e was just inserted, must survive")
}

func TestLRFUCache_DeleteAfterFlush(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](5)
	c.Set("a", 1)
	c.Flush()
	c.Delete("a") // should not panic
	assert.Equal(t, 0, c.Len())
}

func TestLRFUCache_ReinsertAfterDelete(t *testing.T) {
	t.Parallel()
	c := NewLRFUCache[string, int](3)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	c.Delete("b")
	c.Set("b", 99)
	v, ok := c.Get("b")
	require.True(t, ok)
	assert.Equal(t, 99, v)
	assert.Equal(t, 3, c.Len())
}
