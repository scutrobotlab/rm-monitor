package redisx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/pkg/errors"
)

const renewLeaseScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0`

const releaseLeaseScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0`

type Lease struct {
	client *Client
	key    string
	owner  string
	cancel context.CancelFunc
	done   chan struct{}
}

func AcquireLease(ctx context.Context, client *Client, key string, ttl, renewEvery time.Duration) (*Lease, error) {
	if client == nil {
		return nil, errors.New("redis client is required")
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, errors.Wrap(err, "generate lease owner")
	}
	owner := hex.EncodeToString(token[:])
	for {
		ok, err := client.c.SetNX(ctx, key, owner, ttl).Result()
		if err != nil {
			return nil, errors.Wrap(err, "acquire redis lease")
		}
		if ok {
			break
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	leaseCtx, cancel := context.WithCancel(context.Background())
	lease := &Lease{client: client, key: key, owner: owner, cancel: cancel, done: make(chan struct{})}
	go lease.renew(leaseCtx, ttl, renewEvery)
	return lease, nil
}

func (l *Lease) renew(ctx context.Context, ttl, every time.Duration) {
	defer close(l.done)
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = l.client.c.Eval(ctx, renewLeaseScript, []string{l.key}, l.owner, ttl.Milliseconds()).Result()
		}
	}
}

func (l *Lease) Release(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.cancel()
	<-l.done
	_, err := l.client.c.Eval(ctx, releaseLeaseScript, []string{l.key}, l.owner).Result()
	return errors.Wrap(err, "release redis lease")
}
