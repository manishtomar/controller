package controller

// Controller allows clients to trigger control loop for a given key
type Controller interface {

	// TriggerLoop will trigger the control loop associated with given key in some worker which may be this or some other node
	// It returns error if the key cannot be queued to be run.
	TriggerLoop(key string) error
}

// Worker is implemented by controller workers that plan to run the control loop
type Worker interface {

	// RunLoop will be called by controller to run control loop on a given key. Return "true" to stop calling this loop
	// for the given key. Otherwise return "false" which is typically done when the worker is shutdown and loop needs to
	// be scheduled on another worker. "shutdown" channel is closed when the worker needs to shutdown. "trigger" channel is
	// sent data when `controller.TriggerLoop` is called on the same key and the loop is already running on a worker.
	// Controller ensures that this function is
	RunLoop(key string, trigger <-chan struct{}, shutdown <-chan struct{}) bool
}
