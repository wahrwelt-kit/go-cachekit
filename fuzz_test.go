package cachekit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const fuzzMaxStringLen = 1024

func FuzzEscapeRedisGlob(f *testing.F) {
	f.Add("")
	f.Add("user:*")
	f.Add(`user:\*:profile[1]?`)
	f.Add("plain-prefix")

	f.Fuzz(func(t *testing.T, prefix string) {
		if len(prefix) > fuzzMaxStringLen {
			prefix = prefix[:fuzzMaxStringLen]
		}
		escaped := escapeRedisGlob(prefix)
		if hasUnescapedRedisGlobMeta(escaped) {
			t.Fatalf("escaped prefix contains unescaped redis glob meta: %q -> %q", prefix, escaped)
		}
	})
}

func FuzzDeleteByPrefixPattern(f *testing.F) {
	f.Add("user:")
	f.Add("tenant:*")
	f.Add(`prefix\with[meta]?`)

	f.Fuzz(func(t *testing.T, prefix string) {
		if prefix == "" {
			t.Skip()
		}
		if len(prefix) > fuzzMaxStringLen {
			prefix = prefix[:fuzzMaxStringLen]
		}
		client, mock := newRedisClientMock(t)
		c := New(client)
		expectBumpPrefix(mock, prefix).SetVal(1)
		mock.ExpectScan(0, escapeRedisGlob(prefix)+"*", deleteByPrefixBatchSize).SetVal(nil, 0)
		if err := c.DeleteByPrefix(context.Background(), prefix); err != nil {
			t.Fatalf("DeleteByPrefix returned error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("redis expectations: %v", err)
		}
	})
}

func FuzzRedisConfigString(f *testing.F) {
	f.Add("password", 6379, 0, 50, 10)
	f.Add("pa$$word\nwith\tcontrol", -1, -1, -1, -1)

	f.Fuzz(func(t *testing.T, passwordSeed string, port, db, poolSize, minIdle int) {
		sum := sha256.Sum256([]byte(passwordSeed))
		password := "secret-" + hex.EncodeToString(sum[:8])
		cfg := RedisConfig{
			Host:         "localhost",
			Port:         port,
			Password:     password,
			DB:           db,
			PoolSize:     poolSize,
			MinIdleConns: minIdle,
		}
		for _, rendered := range []string{cfg.String(), cfg.GoString()} {
			if strings.Contains(rendered, password) {
				t.Fatalf("RedisConfig rendered password: %q", rendered)
			}
		}
	})
}

func FuzzSieveCacheOperationSequence(f *testing.F) {
	f.Add([]byte{2, 0, 1, 2, 3, 4, 5})
	f.Add([]byte{1, 255, 128, 64, 32, 16})

	f.Fuzz(func(t *testing.T, ops []byte) {
		capacity := 1
		if len(ops) > 0 {
			capacity = int(ops[0]%32) + 1
		}
		c := NewSieveCache[int, int](capacity)
		for i, op := range ops {
			key := int(op % 17)
			switch op % 6 {
			case 0:
				c.Set(key, i)
			case 1:
				c.SetIfAbsent(key, i)
			case 2:
				c.Get(key)
			case 3:
				c.Peek(key)
			case 4:
				c.Delete(key)
			case 5:
				c.Flush()
			default:
				t.Fatalf("unreachable operation: %d", op)
			}
			if c.Len() > c.Cap() {
				t.Fatalf("cache length exceeded capacity: len=%d cap=%d", c.Len(), c.Cap())
			}
		}
	})
}

func FuzzGetOrLoadJSONCacheHit(f *testing.F) {
	f.Add(`{"ok":true}`)
	f.Add(`{"n":1}`)
	f.Add(`not json`)
	f.Add(`[]`)

	f.Fuzz(func(t *testing.T, payload string) {
		if len(payload) > fuzzMaxStringLen {
			payload = payload[:fuzzMaxStringLen]
		}
		var decoded map[string]any
		unmarshalErr := json.Unmarshal([]byte(payload), &decoded)
		client, mock := newRedisClientMock(t)
		c := New(client)
		expectFreshValue(mock, "k", payload)
		if unmarshalErr != nil {
			expectDelKeys(mock, "k").SetVal(2)
		}
		got, err := GetOrLoad(c, context.Background(), "k", time.Minute, func(context.Context) (map[string]any, error) {
			t.Fatal("load function must not be called on redis hit")
			return nil, nil
		})
		switch {
		case unmarshalErr != nil:
			if err == nil {
				t.Fatal("expected unmarshal error")
			}
		case err != nil:
			t.Fatalf("unexpected error: %v", err)
		case got == nil && decoded != nil:
			t.Fatal("expected decoded map")
		default:
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("redis expectations: %v", err)
		}
	})
}

func hasUnescapedRedisGlobMeta(s string) bool {
	escaped := false
	for i := range len(s) {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '*' || ch == '?' || ch == '[' || ch == ']' {
			return true
		}
	}
	return escaped
}
