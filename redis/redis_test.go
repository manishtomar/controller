package redis

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestController(t *testing.T) {
	// setup worker that just sleeps and logs
	w := worker("test worker")

	// setup redis controller
	logger := logrus.New()
	logger.Level = logrus.DebugLevel
	c := New(RedisControllerConfig{
		Prefix:     "test",
		Addr:       "127.0.0.1:6379",
		ID:         "testid",
		Worker:     w,
		MaxWorkers: 5,
		Logger:     logrus.NewEntry(logger),
	})
	c.Start()
	defer c.Stop(time.Second)

	// add some keys
	err := c.TriggerLoop("manish")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error triggering loop: manish: %v", err)
	} else {
		fmt.Println("triggered manish loop")
		time.Sleep(time.Minute)
	}
}

type worker string

func (w worker) RunLoop(key string, trigger <-chan struct{}, shutdown <-chan struct{}) bool {
	for {
		fmt.Printf("running loop - %s\n", key)
		time.Sleep(time.Second)
	}
}
