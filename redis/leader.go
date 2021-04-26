package redis

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

func (r *RedisController) startLeader(shutdown <-chan struct{}) {
	// heartbeat every 3s and expire key every 5s. These are temporary values and needs to be verified
	const heartbeat = 3 * time.Second
	const keyExpiry = 5 * time.Second

	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	stopLeadership := make(chan struct{}, 1)

	toggleLeader := func() {
		if r.tryLeader(keyExpiry) {
			// we became leader. if we were already one then do nothing. if we are new, then start leadership work
			if !r.leader {
				r.logger.Info("became leader")
				r.leader = true
				go r.startLeadership(stopLeadership)
			}
		} else {
			// we lost leadership. if we were already one then stop leadership work
			if r.leader {
				r.logger.Info("lost leadership")
				r.leader = false
				stopLeadership <- struct{}{}
			}
		}
	}

	toggleLeader()
	for {
		select {
		case <-shutdown:
			r.logger.Info("shutting down leader")
			return
		case <-ticker.C:
			toggleLeader()
		}
	}
}

func (r *RedisController) tryLeader(expiry time.Duration) bool {

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	key := r.keyPrefix("leader")

	err := r.rdb.Watch(ctx, func(tx *redis.Tx) error {
		val, err := r.rdb.Get(ctx, key).Result()
		if err == redis.Nil || val == r.id {
			// either leader is not set or we are the leader. in either case, try becoming the leader by setting the value
			val, err = tx.Set(ctx, key, r.id, expiry).Result()
			if err == nil && val == "OK" {
				return nil
			}
		}
		return fmt.Errorf("error getting leader key: %w", err)
	}, key)

	if err != nil {
		if unwrapped := errors.Unwrap(err); unwrapped != nil && unwrapped != redis.Nil {
			r.logger.WithError(err).Error("failed transaction")
		}
		return false
	}

	return true
}

func (r *RedisController) startLeadership(stop <-chan struct{}) {
	go r.distributeKeys(stop)
	go r.startMonitoringWorkers(stop)
}

func (r *RedisController) startMonitoringWorkers(stop <-chan struct{}) {
	// TODO: Think more about the ticker time
	ticker := time.NewTicker(2*time.Second)
	defer ticker.Stop()

	r.monitorWorkers()
	for {
		select {
		case <-stop:
			r.logger.Info("shutting down monitoring workers")
			return
		case <-ticker.C:
			r.monitorWorkers()
		}
	}
}

func (r *RedisController) monitorWorkers() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// get workers
	logger := r.logger.WithField("func", "monitorWorkers")
	workers, err := r.rdb.SMembers(ctx, r.workersKey()).Result()
	if err != nil {
		logger.WithError(err).Error("error getting workers")
		return
	}
	logger.Debugf("got workers: %d, %v", len(workers), workers)

	// find workers that are not alive anymore based on its heartbeat keys
	deadWorkers, err := r.findDeadWorkers(ctx, workers)
	if err != nil {
		logger.WithError(err).Error("error finding dead workers")
		return
	}
	if len(deadWorkers) == 0 {
		return
	}
	logger.Infof("dead workers: %d, %v", len(deadWorkers), deadWorkers)

	// clean up dead workers
	errChan := make(chan error, len(deadWorkers))
	var wg sync.WaitGroup
	for _, deadWorker := range deadWorkers {
		wg.Add(1)
		go func(worker string) {
			defer wg.Done()
			// cleanup keys associated with worker
			err := r.cleanWorkerKeys(worker)
			if err != nil {
				logger.WithError(err).WithField("worker", worker).Error("error moving keys")
				errChan <- err
			}
			// de-register it
			_, err = r.rdb.SRem(ctx, r.workersKey(), worker).Result()
			if err != nil {
				logger.WithError(err).WithField("worker", worker).Error("error de-registering worker")
				errChan <- err
			}
		}(deadWorker)
	}
	wg.Wait()
	select {
	case <-errChan:
		// can't do anything - just return for now. we should be called again
		return
	default:
		if len(deadWorkers) > 0 {
			logger.Infof("cleaned up dead workers: %v", len(deadWorkers))
		}
	}
}

// findDeadWorkers returns workers whose heartbeat keys have expired
func (r *RedisController) findDeadWorkers(ctx context.Context, workers []string) ([]string, error) {
	deadWorkersChan := make(chan string, len(workers))
	errChan := make(chan error, len(workers))
	var wg sync.WaitGroup
	for _, worker := range workers {
		wg.Add(1)
		go func(worker string) {
			defer wg.Done()
			_, err := r.rdb.Get(ctx, r.heartbeatKey(worker)).Result()
			if err == redis.Nil {
				deadWorkersChan <- worker
			} else if err != nil {
				errChan <- fmt.Errorf("error getting heartbeat of %s worker: %w", worker, err)
			}
		}(worker)
	}
	wg.Wait()
	close(deadWorkersChan)

	select {
	case err := <-errChan:
		return nil, err
	default:
	}
	deadWorkers := make([]string, 0)
	for dw := range deadWorkersChan {
		deadWorkers = append(deadWorkers, dw)
	}
	return deadWorkers, nil
}

func (r *RedisController) cleanWorkerKeys(worker string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	globalQueue := r.globalQueue()

	moveKeys := func(fromSet string) error {
		for {
			// get subset of keys from set
			keys, err := r.rdb.SRandMemberN(ctx, fromSet, 10).Result()
			if err != nil {
				return err
			}
			if len(keys) == 0 {
				// no more keys
				break
			}

			// move them to global q
			for _, key := range keys {
				_, err := r.rdb.SMove(ctx, fromSet, globalQueue, key).Result()
				if err != nil {
					return err
				}
			}
		}
		return nil
	}

	err := moveKeys(r.runQueue(worker))
	if err != nil {
		return err
	}
	return moveKeys(r.runningSet(worker))
}

func (r *RedisController) distributeKeys(stop <-chan struct{}) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			r.processGlobalQueue()
		}
	}
}

func (r *RedisController) processGlobalQueue() {
	globalQueueKey := r.globalQueue()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		// get key from global queue
		key, err := r.rdb.SRandMember(ctx, globalQueueKey).Result()
		if err == redis.Nil {
			// no more keys; return for now.
			return
		}
		if err != nil {
			r.logger.WithError(err).Error("error getting key from global queue")
			return
		}
		logger := r.logger.WithField("key", key).WithField("func", "processGlobalQueue")
		logger.Debug("processing key")

		// find a suitable worker to distribute this key; the key may already being processed by existing worker
		worker, err := r.findWorkerForKey(key)
		if err != nil {
			logger.WithError(err).Error("error finding worker for key")
			return
		}
		logger = logger.WithField("worker", worker)
		logger.Debug("found worker")

		// add key to worker's runqueue
		added, err := r.rdb.SAdd(ctx, r.runQueue(worker), key).Result()
		if err != nil {
			logger.WithError(err).Error("error adding key to worker")
			return
		}
		if added != 1 {
			// This means key was already in the set which should never happen. However, we can continue processing
			// log the anomaly
			logger.WithField("added", added).Warn("key already in worker")
		}
		logger.Debug("added key to worker runqueue")

		// remove key from global queue
		_, err = r.rdb.SRem(ctx, globalQueueKey, key).Result()
		if err != nil {
			logger.WithError(err).Error("error removing key from globalq")
		}
		logger.Debug("removed key from globalq")
	}
}

// findWorkerForKey will find a worker for given key
func (r *RedisController) findWorkerForKey(key string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	logger := r.logger.WithField("func", "findWorkerForKey")

	// load workers
	workers, err := r.rdb.SMembers(ctx, r.workersKey()).Result()
	if err != nil {
		return "", err
	}
	if len(workers) == 1 {
		logger.Debugf("returning only 1 worker: %s", workers[0])
		return workers[0], nil
	}
	logger.Debugf("got workers: %v", workers)

	// find key in worker's runqueue and runlist
	result := make(chan string, 1)
	errChan := make(chan error, len(workers))
	var wd sync.WaitGroup
	for _, worker := range workers {
		wd.Add(1)
		go func(worker string) {
			defer wd.Done()
			isMember, err := r.rdb.SIsMember(ctx, r.runQueue(worker), key).Result()
			if err != nil {
				errChan <- err
				return
			}
			if isMember {
				result <- worker
				return
			}
			isMember, err = r.rdb.SIsMember(ctx, r.runningSet(worker), key).Result()
			if err != nil {
				errChan <- err
				return
			}
			if isMember {
				result <- worker
				return
			}
		}(worker)
	}
	wd.Wait()
	logger.Debug("finished waiting to find key in existing workers")

	select {
	case err := <-errChan:
		// got error when trying to find the worker; return it
		return "", err
	case worker := <-result:
		// got worker already processing the key; return it
		logger.Debugf("key already exist in worker: %s", worker)
		return worker, nil
	default:
	}
	logger.Debug("did not find worker - gonna check lengths")

	// find length of workers runqueue and runlist if we didn't find key in existing workers
	type workerLength struct {
		worker string
		length int64
	}
	lengths := make(chan workerLength, len(workers))
	for _, worker := range workers {
		wd.Add(1)
		go func(worker string) {
			defer wd.Done()
			queueLength, err := r.rdb.SCard(ctx, r.runQueue(worker)).Result()
			if err != nil {
				errChan <- err
				return
			}
			runningLength, err := r.rdb.SCard(ctx, r.runningSet(worker)).Result()
			if err != nil {
				errChan <- err
				return
			}
			lengths <- workerLength{
				worker: worker,
				length: queueLength + runningLength,
			}
		}(worker)
	}
	wd.Wait()
	close(lengths)
	logger.Debug("got lengths")

	select {
	case err := <-errChan:
		// got error when trying to find the worker; return it
		return "", err
	default:
	}
	logger.Debug("no errors checking lengths")

	// decide a worker with smallest runqueue and running set
	// NOTE: This is currently naive implementation of scheduling the worker. Will be nice to consider runqueue separately
	// when deciding. Ideally runqueue should always be near to 0 for every worker. If it isn't then something is wrong
	// and that worker should be avoided.
	minWorker := workerLength{length: 9223372036854775807}
	for wl := range lengths {
		if wl.length <= minWorker.length {
			minWorker = wl
		}
		logger.Debugf("lengths loop - wl: %v, low worker: %s", wl, minWorker.worker)
	}
	return minWorker.worker, nil
}