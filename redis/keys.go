package redis

import "fmt"

func (r *RedisController) keyPrefix(key string) string {
	return fmt.Sprintf("controller:%s:%s", r.prefix, key)
}

func (r *RedisController) globalQueue() string {
	return r.keyPrefix("global")
}

func (r *RedisController) runQueue(worker string) string {
	return r.keyPrefix("node:runqueue:" + worker)
}

func (r *RedisController) runningSet(worker string) string {
	return r.keyPrefix("node:running:" + worker)
}

func (r *RedisController) workersKey() string {
	return r.keyPrefix("workers")
}

func (r *RedisController) heartbeatKey(worker string) string {
	return r.keyPrefix("workerheartbeat:" + worker)
}