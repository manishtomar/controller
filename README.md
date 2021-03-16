# Background

Many software systems manage resources in other systems. The biggest example is public cloud where a VM is created on an API call and it is managed by the cloud service. Typically these systems are implemented as a control loop per resource where each loop compares the current state with desired state and does actions that bring the actual state closer to desired state. This repo provides a framework to help build such systems.

# Solution

Implementing these systems involves running a control loop somewhere for each resource. This repo provides a framework in go for running that control loop in a reliable, scalable manner. For a given key which would typically be some kind of identifier for the resource being managed "controller" will call a function in a separate goroutine in any one of the processes/nodes. controller guarantees that for a given key only one goroutine will be called. For example if controller is started in 2 nodes then every time `TriggerLoop(key)` is called a function is called with that key in either one of the two nodes. The framework balances number of goroutines among the nodes. The nodes can be added and removed as needed and controller will automatically scale accordingly. If a loop is shutdown in one node it is automatically started in another. controller tries very hard to ensure that loop for any given key us always run and run only once. This is the desired property of the system and will be tested as itis implemented.

Note that while this framework fits to implement a control loop that is by no means the only use of this API. It can be used anywhere which requires running a goroutine per key/string.

Following is sample example of its usage:
```go
func main() {
  ...
  redisConf := RedisControllerConf{
    Host: "redis.private.company.com",
    Port: 335,
    MaximumLoops: 1000,
  }
  controller := NewRedisController(&conf)
  controller.SetLoop(service) // set service.Loop to be called when TriggerLoop is called
  controller.Start()  // controller will be ready to trigger loops in any worker connecting with this configuration
  defer controller.Stop(60*time.Second) // Stop will tell each loop to shutdown
  ...
}

// Somewhere later when loop needs to be triggered when a resource is created
func (s *Service) CreateResource(ctx context.Context.Context, req *CreateResourceRequest) error {
  res := createResource(req)
  if err := s.controller.TriggerLoop(res.id); err != nil {
    s.logger.Error("error triggering loop")
    return err
  }
  return nil
}

// Loop implementation
func (s *Service) Loop(key string, shutdown <-chan struct{}, trigger <-chan struct{}) Decision {
  t := time.NewTicker(1*time.Second)
  completed := make(chan struct{}, 1)
  for {
    select {
    case <-shutdown:
      return Continue
    case <-completed:
      return Done
    case <-trigger:
    case <-ticker.C:
      done := s.resourceLoop(key)
      if done {
        completed <- struct{}{}
      }
    }
  }
}
```

# API

```go

type Decision int

// Complete is returned when the Loop for the given key is no longer required.
// This would be typically when a resource representing the key has been completely deleted and is no longer need to be monitored
const Complete Decision = 0

// Continue is returned when the Loop needs to be continued in another process.
const Continue Decision = 1

type Controller interface {

  // Loop is called by the framework when `TriggerLoop(key)` is called on the given key.
  // It takes "shutdown" channel that is closed when the function is supposed to persist its state and return as quickly as possible.
  // This typically happens the process calling this function is shutting down.
  // "trigger" channel is sent empty data every time `TriggerLoop(key)` is called with this key and the function is already running.
  // The function must return a value that informs whether this loop should be continued in another process.
  func Loop(key string, shutdown <-chan struct{}, trigger <-chan struct{}) Decision
}

// ContextFromShutdown returns a context that is cancelled when the shutdown channel is closed. Use it inside Loop to cancel
// operations happening on external systems during shutdown
func ContextFromShutdown(shutdown <-chan struct{}) context.Context {
}
```

# Redis controller
This section documents implementation of controller with redis backend. It uses redis to store the keys and coordinate the loop calling among multiple nodes.
Each node connecting to redis instance will trigger loops in goroutines. One of the nodes will be elected leader which will do some extra book-keeping apart from running loops.

Each controller node is provided a unique ID. This can be a random UUID but having something that easily identifies the node/process should be preferred. It can be container ID if you are running this in containers or it can be IP address run in a VM.

## Leader node:
The leader will be elected by setting a well known key in redis and resetting it again at an interval. It will heartbeat to advertise its leadership. Other nodes will keep trying to make themselves leader in case the leader goes down and stops heartbeating. It does this by using `WATCH` command. Following is sample code:
```
WATCH controller:leader
GET controller:leader
If key is empty
MULTI
SET controller:leader leaderid EX 5
EXEC
```
Here each node tries to set a key under a `WATCH` command. It will do so only if key is empty. One of the nodes running this will be able to set and others will see `EXEC` fail since the key would've been modified.

### Responsibilities
Leader will have following responsibilities:

**Monitor other nodes**: Each node will heartbeat itself by setting a key with its name. The key will have expiry that is greater than heartbeat interval. If the key disappers then the node is removed from list of running nodes and its running and to be run keys will be distributed to other nodes.

**Distribute the keys**: When `TriggerLoop(key)` is called the key is added to list of keys that need to be scheduled. The leader will read the key from the list and decide which node should run it with goal of balancing the load. Once decided, it will add it that node's list.
