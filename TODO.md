## Backends
* Think about postgres

## Feature
* Have function to stop a loop: `StopLoop`
* Consider sending a generic message to global queue that will be processed by leader. It may contain `trigger <key>` or `stop <key>` messages and it is stored as a list to be used strictly as a queue. However, how should worker queues be managed? Should there be two sets - one for runq and another for stopq? Or one set with `run:id` and `stop:id`. This way it is one set and allows to be searched.

## Code
* Use `errgroup` when doing something with workers
* Can generics be used anywhere?
* Think about failure cases:
    - Redis restarting loosing everything. how does running controller get affected?
    - Is it possible to lose a loop?
    - Running a loop on multiple workers simultaneously?
* Better unit and integration tests.
    - Consider abstracting redis calls to interface for unit testing.
    - Consider abstracting timing to an interface to simulate timing in unit testing.