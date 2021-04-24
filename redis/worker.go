package redis

import (
	"context"
	"errors"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/manishtomar/controller"
	"github.com/sirupsen/logrus"
)

func (r *RedisController) startWorker(shutdown <-chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// register worker in redis set
	_, err := r.rdb.SAdd(ctx, r.workersKey(), r.id).Result()
	if err != nil {
		// cannot start processing until worker is registered. log and return as of now
		// TODO: keep trying forever with backoff since we want the system to be resilient of network failures
		r.logger.WithError(err).Error("error registering")
		return	// temp
	}

	go r.heartbeatWorker(shutdown)
	go r.runLoops(shutdown)
}

func (r *RedisController) heartbeatWorker(shutdown <- chan struct{}) {

	// heartbeat every 3s and expire key every 5s. These are temporary values and needs to be verified
	const heartbeatInterval = 3 * time.Second
	const keyExpiry = 5 * time.Second

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	start := make(chan struct{}, 1)
	start <- struct{}{}

	heartbeat := func() {
		key := r.keyPrefix("workerheartbeat:" + r.id)
		// set key with expiry
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		val, err := r.rdb.Set(ctx, key, time.Now().String(), keyExpiry).Result()
		if err != nil || val != "OK" {
			r.logger.WithError(err).WithFields(logrus.Fields{
				"key": key,
				"val": val,
			}).Error("error heartbeating worker")
		}
	}

	for {
		select {
		case <-shutdown:
			return
		case <-start:
			heartbeat()
		case <-ticker.C:
			heartbeat()
		}
	}
}

func (r *RedisController) runLoops(shutdown <- chan struct{}) {
	ws := &workersSet{
		worker:        r.worker,
		maxWorkers: r.maxWorkers,
		running: make(map[string]chan struct{}),
		stop: shutdown,	// TODO: should it another channel?
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-shutdown:
			return
		case <-ticker.C:
			r.processRunQueue(ws)
		}
	}
}

// processRunQueue will process all keys in run queue and will return when there are none
func (r *RedisController) processRunQueue(ws *workersSet) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	runQueueKey := r.runQueue(r.id)
	for {
		key, err := r.rdb.SRandMember(ctx, runQueueKey).Result()
		if err == redis.Nil {
			// no more keys; return
			return
		}
		if err != nil {
			// just log and return
			r.logger.WithError(err).Error("worker - error getting key from queue")
			return
		}
		logger := r.logger.WithField("key", key)
		logger.Debug("worker - got key")
		if err := ws.addKey(key); err != nil {
			// just log and return
			logger.WithError(err).Error("worker - error triggering loop")
			return
		}
		logger.Debug("worker - triggered loop")

		// the workerset accepted the key; add it to redis running set
		_, err = r.rdb.SAdd(ctx, r.runningSet(r.id), key).Result()
		if err != nil {
			// log and return
			logger.WithError(err).Error("worker - error adding to running set")
			return
		}
		logger.Debug("added to running set")

		// remove the key from run queue now that it is running
		removed, err := r.rdb.SRem(ctx, runQueueKey, key).Result()
		if err != nil || removed != 1 {
			// log and return
			logger.WithError(err).WithField("removed", removed).Error("worker - error removing from run queue")
			return
		}
		logger.Debug("removed from run queue")
	}
}

// workersSet runs a set for workers where each worker is tied to a key
type workersSet struct {
	worker controller.Worker
	maxWorkers int
	running map[string]chan struct{}
	stop <-chan struct{}
}

func (w *workersSet) addKey(key string) error {
	if len(w.running) >= w.maxWorkers {
		return errors.New("max workers reached")
	}

	// trigger the worker goroutine
	trigger, ok := w.running[key]
	if ok {
		trigger <- struct {}{}
	} else {
		trigger = make(chan struct{})
		go func() {
			w.worker.RunLoop(key, trigger, w.stop)
			// TODO: Capture panic, log it and decide what to do with it
			// TODO: Handle key deletion based on RunLoop return value
		}()
		w.running[key] = trigger
	}

	return nil
}