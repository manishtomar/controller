# Background

Many software systems manage resources in other systems. The biggest example is public cloud where a VM is created on an API call and it is managed by the cloud service. Typically these systems are implemented as a control loop per resource where each loop compares the current state with desired state and does actions that bring the actual state closer to desired state.

Implementing these systems involves running a control loop somewhere for each resource. This repo provides a framework in go for running that control loop in a reliable, scalable manner. It provides an API to be consumed by implementer of such system and one or more implementations that call that API. Note that while this framework fits to implement a control loop that is by no means the only use of this API. It can be used anywhere which requires running a goroutine per key/string.

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

