package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/manishtomar/controller"
	"github.com/sirupsen/logrus"
)

// RedisController implements Controller interface with redis backend
type RedisController struct {
	prefix string
	id string
	worker controller.Worker
	maxWorkers int
	logger *logrus.Entry

	// running config
	rdb *redis.Client
	leader bool	// is this leader node?
	shutdown chan struct{}	// channel closed when controller is shutting down
}

type RedisControllerConfig struct {
	Prefix string
	Addr string
	Password string
	ID string
	Worker controller.Worker
	MaxWorkers int
	Logger *logrus.Entry
}

func New(conf RedisControllerConfig) *RedisController {
	return &RedisController{
		prefix: conf.Prefix,
		id:     conf.ID,
		worker: conf.Worker,
		maxWorkers: conf.MaxWorkers,
		logger: conf.Logger.WithField("module", "rediscontroller").WithField("controller_id", conf.ID),
		rdb: redis.NewClient(&redis.Options{
			Addr:     conf.Addr,
			Password: conf.Password,
			DB:       0,  // use default DB
		}),
		shutdown: make(chan struct{}),
		leader: false,
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

func (r *RedisController) TriggerLoop(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// add key to global queue
	globalQueue := r.keyPrefix("global")
	_, err := r.rdb.RPush(ctx, globalQueue, key).Result()
	if err != nil {
		return fmt.Errorf("error inserting key to global queue: %w", err)
	}
	return nil
}

func (r *RedisController) keyPrefix(key string) string {
	return fmt.Sprintf("controller:%s:%s", r.prefix, key)
}
