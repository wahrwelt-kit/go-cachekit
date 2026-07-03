package cachekit

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func nilContext() context.Context {
	return nil
}

type redisExpectation struct {
	name     string
	validate func([]any) error
	apply    func(redis.Cmder) error
}

type redisMock struct {
	t            testing.TB
	mu           sync.Mutex
	expectations []redisExpectation
}

type redisMockExpectation struct {
	exp *redisExpectation
}

func newRedisClientMock(tb testing.TB) (*redis.Client, *redisMock) {
	tb.Helper()
	mock := &redisMock{t: tb}
	client := redis.NewClient(&redis.Options{
		Addr:       "cachekit-test-redis.invalid:6379",
		MaxRetries: -1,
	})
	client.AddHook(mock)
	tb.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			tb.Fatalf("redis expectations: %v", err)
		}
		_ = client.Close()
	})
	return client, mock
}

func (m *redisMock) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (m *redisMock) ProcessHook(_ redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if len(m.expectations) == 0 {
			err := fmt.Errorf("unexpected redis command %q with args %v", cmd.Name(), cmd.Args())
			cmd.SetErr(err)
			return err
		}
		exp := m.expectations[0]
		m.expectations = m.expectations[1:]
		if cmd.Name() != exp.name {
			err := fmt.Errorf("unexpected redis command %q, want %q", cmd.Name(), exp.name)
			cmd.SetErr(err)
			return err
		}
		if exp.validate != nil {
			if err := exp.validate(cmd.Args()); err != nil {
				cmd.SetErr(err)
				return err
			}
		}
		if exp.apply == nil {
			return nil
		}
		return exp.apply(cmd)
	}
}

func (m *redisMock) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (m *redisMock) ExpectationsWereMet() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.expectations) == 0 {
		return nil
	}
	return fmt.Errorf("%d redis expectations were not met; next command %q", len(m.expectations), m.expectations[0].name)
}

func (m *redisMock) addExpectation(exp redisExpectation) *redisMockExpectation {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expectations = append(m.expectations, exp)
	return &redisMockExpectation{exp: &m.expectations[len(m.expectations)-1]}
}

func (m *redisMock) ExpectGet(key string) *redisMockExpectation {
	return m.addExpectation(redisExpectation{
		name: "get",
		validate: func(args []any) error {
			return expectArgs(args, "get", key)
		},
	})
}

func (m *redisMock) ExpectSet(key string, value any, ttl time.Duration) *redisMockExpectation {
	return m.addExpectation(redisExpectation{
		name: "set",
		validate: func(args []any) error {
			if len(args) < 3 {
				return fmt.Errorf("set args too short: %v", args)
			}
			if err := expectArgs(args[:3], "set", key, value); err != nil {
				return err
			}
			if ttl > 0 && !containsDurationArg(args[3:], ttl) {
				return fmt.Errorf("set ttl %v not found in args %v", ttl, args)
			}
			return nil
		},
	})
}

func (m *redisMock) ExpectScript(script string, keys []string, args ...any) *redisMockExpectation {
	return m.addExpectation(redisExpectation{
		name: "evalsha",
		validate: func(got []any) error {
			want := make([]any, 0, 3+len(keys)+len(args))
			want = append(want, "evalsha", redis.NewScript(script).Hash(), len(keys))
			for _, key := range keys {
				want = append(want, key)
			}
			want = append(want, args...)
			return expectArgs(got, want...)
		},
	})
}

func (m *redisMock) ExpectDel(keys ...string) *redisMockExpectation {
	want := make([]any, 0, len(keys)+1)
	want = append(want, "del")
	for _, key := range keys {
		want = append(want, key)
	}
	return m.addExpectation(redisExpectation{
		name: "del",
		validate: func(args []any) error {
			return expectArgs(args, want...)
		},
	})
}

func (m *redisMock) ExpectScan(cursor uint64, match string, count int64) *redisMockExpectation {
	return m.addExpectation(redisExpectation{
		name: "scan",
		validate: func(args []any) error {
			if len(args) != 6 {
				return fmt.Errorf("scan args mismatch: %v", args)
			}
			if err := expectArgs(args[:2], "scan", cursor); err != nil {
				return err
			}
			if !strings.EqualFold(fmt.Sprint(args[2]), "match") || fmt.Sprint(args[3]) != match {
				return fmt.Errorf("scan match mismatch: %v", args)
			}
			if !strings.EqualFold(fmt.Sprint(args[4]), "count") || fmt.Sprint(args[5]) != fmt.Sprint(count) {
				return fmt.Errorf("scan count mismatch: %v", args)
			}
			return nil
		},
	})
}

func (e *redisMockExpectation) SetVal(value any, extra ...uint64) *redisMockExpectation {
	e.exp.apply = func(cmd redis.Cmder) error {
		switch c := cmd.(type) {
		case *redis.StringCmd:
			c.SetVal(fmt.Sprint(value))
		case *redis.StatusCmd:
			c.SetVal(fmt.Sprint(value))
		case *redis.IntCmd:
			switch v := value.(type) {
			case int:
				c.SetVal(int64(v))
			case int64:
				c.SetVal(v)
			default:
				return fmt.Errorf("unsupported int value %T", value)
			}
		case *redis.ScanCmd:
			var keys []string
			switch v := value.(type) {
			case nil:
			case []string:
				keys = v
			default:
				return fmt.Errorf("unsupported scan keys value %T", value)
			}
			var cursor uint64
			if len(extra) > 0 {
				cursor = extra[0]
			}
			c.SetVal(keys, cursor)
		case *redis.Cmd:
			if v, ok := value.(int); ok {
				c.SetVal(int64(v))
			} else {
				c.SetVal(value)
			}
		default:
			return fmt.Errorf("unsupported redis command type %T", cmd)
		}
		return nil
	}
	return e
}

func (e *redisMockExpectation) SetErr(err error) *redisMockExpectation {
	e.exp.apply = func(cmd redis.Cmder) error {
		cmd.SetErr(err)
		return err
	}
	return e
}

func expectArgs(got []any, want ...any) error {
	if len(got) != len(want) {
		return fmt.Errorf("args length mismatch: got %v want %v", got, want)
	}
	for i := range want {
		if !argEqual(got[i], want[i]) {
			return fmt.Errorf("arg %d mismatch: got %v (%T), want %v (%T)", i, got[i], got[i], want[i], want[i])
		}
	}
	return nil
}

func argEqual(got, want any) bool {
	if reflect.DeepEqual(got, want) {
		return true
	}
	if gotBytes, ok := got.([]byte); ok {
		if wantBytes, ok := want.([]byte); ok {
			return bytes.Equal(gotBytes, wantBytes)
		}
		return string(gotBytes) == fmt.Sprint(want)
	}
	if wantBytes, ok := want.([]byte); ok {
		return fmt.Sprint(got) == string(wantBytes)
	}
	return fmt.Sprint(got) == fmt.Sprint(want)
}

func containsDurationArg(args []any, want time.Duration) bool {
	wantSeconds := int64(want / time.Second)
	for _, arg := range args {
		if d, ok := arg.(time.Duration); ok && d == want {
			return true
		}
		if fmt.Sprint(arg) == want.String() {
			return true
		}
		if wantSeconds > 0 && fmt.Sprint(arg) == fmt.Sprint(wantSeconds) {
			return true
		}
	}
	return false
}
