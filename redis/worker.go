package redis

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"
)

func (r *RedisController) startWorker(shutdown <-chan struct{}) {
	loopRunner := newRunner(r)
	loopRunner.start(shutdown)
	go r.heartbeatWorker(shutdown, loopRunner)
}

func (r *RedisController) registerWorker() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// TODO: keep trying forever with backoff since we want the system to be resilient of network failures
	_, err := r.rdb.SAdd(ctx, r.workersKey(), r.id).Result()
	return err
}

func (r *RedisController) heartbeatWorker(shutdown <- chan struct{}, lr *runner) {

	// heartbeat every 3s and expire key every 5s. These are temporary values and needs to be verified
	const heartbeatInterval = 3 * time.Second
	const keyExpiry = 5 * time.Second

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	// start off inactive - will become active when heartbeat succeeds
	active := false

	heartbeat := func() {
		key := r.heartbeatKey(r.id)
		// set key with expiry
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		val, err := r.rdb.Set(ctx, key, time.Now().String(), keyExpiry).Result()
		if err != nil || val != "OK" {
			r.logger.WithError(err).WithFields(logrus.Fields{
				"key": key,
				"val": val,
			}).Error("error heartbeating worker")

			// we are not active anymore - inform runner to pause running loops
			lr.pause()
			active = false
		} else {
			// we are active now - inform runner to resume running loops if we were inactive
			if !active {
				if err := r.registerWorker(); err != nil {
					r.logger.WithError(err).Error("error registering after becoming active")
					return
				}
				lr.resume()
				active = true
			}
		}
	}

	heartbeat()
	for {
		select {
		case <-shutdown:
			r.logger.Info("shutting down worker heartbeat")
			return
		case <-ticker.C:
			heartbeat()
		}
	}
}

// runner runs all the worker loops
type runner struct {
	r *RedisController
	running map[string]chan struct{}
	stopRunning chan struct{}
	startRunning chan struct{}
	paused bool
	logger *logrus.Entry
}

func newRunner(r *RedisController) *runner {
	return &runner{
		r:          r,
		running:    make(map[string]chan struct{}),
		stopRunning:       make(chan struct{}),
		startRunning: make(chan struct{}),
		paused: false,
		logger : r.logger.WithField("module", "runner"),
	}
}

// start will start processing worker queue and run the loops
func (r *runner) start(shutdown <-chan struct{}) {
	go r.runLoops(shutdown)
}

func (r *runner) runLoops(shutdown <-chan struct{}) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-shutdown:
			r.logger.Info("shutting down running loops")
			close(r.stopRunning)
			return
		case <-r.stopRunning:
			if !r.paused {
				r.logger.Info("instructed to stop running")
			}
			r.paused = true
		case <-r.startRunning:
			if len(r.running) != 0 {
				// ideally shouldn't have been running loops
				r.logger.Warnf("still running %d loops when asked to resume", len(r.running))
			}
			r.paused = false
			r.stopRunning = make(chan struct{})
			r.logger.Info("instructed to start running")
		case <-ticker.C:
			if !r.paused {
				r.processRunQueue()
			}
		}
	}
}

// pause will temporarily stop processing worker and inform all the currently running loops to stop
func (r *runner) pause() {
	if !r.paused {
		close(r.stopRunning)
	}
}

// resume will resume processing loops. will be noop if it is already processing
func (r *runner) resume() {
	r.startRunning <- struct{}{}
}

// processRunQueue will process all keys in run queue and will return when there are none
func (r *runner) processRunQueue() {

	if len(r.running) == r.r.maxWorkers {
		r.logger.WithField("max_workers", r.r.maxWorkers).Error("max workers reached")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	runQueueKey := r.r.runQueue(r.r.id)
	for {
		key, err := r.r.rdb.SRandMember(ctx, runQueueKey).Result()
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
		r.addKey(key, r.logger)
		logger.Debug("worker - triggered loop")

		// the workerset accepted the key; add it to redis running set
		_, err = r.r.rdb.SAdd(ctx, r.r.runningSet(r.r.id), key).Result()
		if err != nil {
			// log and return
			logger.WithError(err).Error("worker - error adding to running set")
			return
		}
		logger.Debug("added to running set")

		// remove the key from run queue now that it is running
		removed, err := r.r.rdb.SRem(ctx, runQueueKey, key).Result()
		if err != nil || removed != 1 {
			// log and return
			logger.WithError(err).WithField("removed", removed).Error("worker - error removing from run queue")
			return
		}
		logger.Debug("removed from run queue")
	}
}

func (r *runner) addKey(key string, logger *logrus.Entry) {

	// trigger the worker goroutine
	trigger, ok := r.running[key]
	if ok {
		select {
		case trigger <- struct {}{}:
		default:
			logger.WithField("key", key).Warn("couldn't send on trigger channel since it is already blocked")
		}
	} else {
		trigger = make(chan struct{})
		go func() {
			r.r.worker.RunLoop(key, trigger, r.stopRunning)
			// TODO: Capture panic, log it and decide what to do with it
			// TODO: Handle key deletion based on RunLoop return value and remove it from w.running after function returns
			// TODO: Wrap it in waitgroup to know how many are running
		}()
		r.running[key] = trigger
	}
}