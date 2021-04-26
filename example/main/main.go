package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/manishtomar/controller/redis"
	"github.com/sirupsen/logrus"
)

func main() {
	// setup worker that just sleeps and logs
	w := worker("test worker")

	// setup redis controller
	logger := logrus.New()
	logger.Level = logrus.DebugLevel
	c := redis.New(redis.RedisControllerConfig{
		Prefix:     "test",
		Addr:       "127.0.0.1:6379",
		ID:         os.Args[1],
		Worker:     w,
		MaxWorkers: 100,
		Logger: logrus.NewEntry(logger),
	})
	c.Start()
	defer c.Stop(time.Second)

	// sample http server to trigger new keys
	http.HandleFunc("/trigger", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "only POST allowed", http.StatusMethodNotAllowed)
			return
		}
		err := r.ParseForm()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		keys, ok := r.PostForm["key"]
		if !ok {
			http.Error(w, "key param not found", http.StatusBadRequest)
			return
		}
		for _, key := range keys {
			err = c.TriggerLoop(key)
			if err != nil {
				resp := fmt.Sprintf("error triggering loop for key %s: %v", key, err)
				http.Error(w, resp, http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := http.ListenAndServe(fmt.Sprintf(":%s", os.Args[1]), nil); err != nil {
		fmt.Fprintf(os.Stderr, "error starting http server: %v", err)
		return
	}
	fmt.Println("started http server")
}

type worker string

func (w worker) RunLoop(key string, trigger chan struct{}, shutdown <-chan struct{}) bool {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-trigger:
			fmt.Printf("[%s] got triggered\n", key)
		case <-shutdown:
			fmt.Printf("[%s] shutting down\n", key)
			return false
		case <-ticker.C:
			fmt.Printf("[%s] running loop\n", key)
		}
	}
}