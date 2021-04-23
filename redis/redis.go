package redis

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v8"
	"go/ast"
	"time"
)

// RedisController implements Controller interface with redis backend
type RedisController struct {
	prefix string
	id string

	// running config
	rdb *redis.Client
	leader bool	// is this leader node?
	shutdown chan struct{}	// channel closed when controller is shutting down
}

type RedisControllerConfig struct {
	Prefix string
	Host string
	Port string
	Password string
	ID string
}

func New(conf RedisControllerConfig) *RedisController {
	return &RedisController{
		prefix: conf.Prefix,
		id:     conf.ID,
		rdb: redis.NewClient(&redis.Options{
			Addr:     conf.Host,
			Password: conf.Password,
			DB:       0,  // use default DB
		}),
		shutdown: make(chan struct{}),
	}
}

func (r *RedisController) Start() {
	go r.startLeader(r.shutdown)
	go r.startWorker(r.shutdown)
}

func (r *RedisController) Stop(timeout time.Duration) {
	close(r.shutdown)
	// wait for "timeout" before returning
}

func (r *RedisController) keyPrefix(key string) string {
	return fmt.Sprintf("controller:%s:%s", r.prefix, key)
}

func (r *RedisController) startLeader(shutdown <-chan struct{}) {
	// heartbeat every 3s and expire key every 5s. These are temporary values and needs to be verified
	const heartbeat = 3 * time.Second
	const keyExpiry = 5 * time.Second

	ticker := time.NewTicker(heartbeat)
	stopLeadership := make(chan struct{}, 1)
	for {
		select {
		case <-shutdown:
			return
		case <-ticker.C:
			if r.tryLeader(keyExpiry) {
				// we became leader. if we were already one then do nothing. if we are new, then start leadership work
				if !r.leader {
					r.leader = true
					go r.startLeadership(stopLeadership)
				}
			} else {
				// we lost leadership. if we were already one then stop leadership work
				r.leader = false
				stopLeadership <- struct{}{}
			}
		}
	}
}

func (r *RedisController) tryLeader(expiry time.Duration) bool {

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	key := r.keyPrefix("leader")

	err := r.rdb.Watch(ctx, func(tx *redis.Tx) error {
		val, err := r.rdb.Get(ctx, key).Result()
		if err != nil {
			// error; return
			return fmt.Errorf("error getting leader key: %w", err)
		}
		if err == redis.Nil || val == r.id {
			// either leader is not set or we are the leader. in either case, try becoming the leader by setting the value
			val, err = tx.Set(ctx, key, r.id, expiry).Result()
			if err == nil && val == "OK" {
				return nil
			}
		}
		return errors.New("not leader")
	}, key)

	return err == nil
}