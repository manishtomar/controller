package redis

import "context"

// Queue provides methods to process messages from a queue.
interface Queue {

	// AddKey adds key to bottom of the queue
	AddKey(ctx context.Context, key string) error
	
	// GetKey returns any key from the queue. The key may not be at the top of the queue and
	// calling again may not return the same key. It must be deleted using RemoveKey after processing it.
	GetKey(ctx context.Context) (string, error)

	// RemoveKey removes key from the queue.
	RemoveKey(ctx context.Context, key string) error
}

// LeadershipNotifier notifies about when this node's leadership changes
interface LeadershipNotifier {
	BecameLeader(ctx context.Context)
	LostLeadership(ctx context.Context)
}

interface Workers {
	FindWorkerWithKey(ctx context.Context, key string) (string, error)

	// GetLeastLoadWorker returns worker with least number of keys assigned to it.
	GetLeastLoadWorker(ctx context.Context) (string, error)

	// GetWorkerQueue will return queue tied to the given worker
	GetWorkerQueue(ctx context.Context, worker string) Queue

	HeartbeatWorker(ctx context.Context, worker string) error

	// AddWorker adds a worker.
	// TODO: this may not be required if HeartbeatWorker does this automatically. this is effect of redis setup
	AddWorker(ctx context.Context, worker string) error
}