package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	ds timestampHandler
)

func init() {
	ds = &dataStore{}
}

func main() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	// start the HTTP Server
	go startHTTPServer()
	// store and retrieve by Client

	<-sigCh
}

type timestampHandler interface {
	store(ts int64)
	get() time.Time
}

// DataStore stores a unix timestamp in a time.Time format, concurrency safely
// panics if an attempt to an operation to an unitialized DataStore happens
type dataStore struct {
	ts        time.Time
	writeFlag uint32
}

func (ds *dataStore) setWrite() {
	if ds == nil {
		panic("writing to uninitialized dataStore")
	}
	atomic.StoreUint32(&ds.writeFlag, 1)
}

func (ds *dataStore) unsetWrite() {
	if ds == nil {
		panic("writing to uninitialized dataStore")
	}
	atomic.StoreUint32(&ds.writeFlag, 0)
}

func (ds *dataStore) store(ts int64) {
	if ds == nil {
		panic("writing to uninitialized dataStore")
	}
	ds.setWrite()
	defer ds.unsetWrite()
	ds.ts = time.Unix(ts, 0)
}

func (ds *dataStore) get() time.Time {
	if ds == nil {
		panic("reading from uninitialized dataStore")
	}
	var readable uint32
	for atomic.LoadUint32(&ds.writeFlag) != readable {
	}
	return ds.ts
}

// HTTP handlers
func store(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

}

func retrieve(w http.ResponseWriter, r *http.Request) {

}

// helpers
func startHTTPServer() {
	http.HandleFunc("/store", store)
	http.HandleFunc("/retrieve", retrieve)
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", nil))
}

type incomingTs string

func (its incomingTs) validate() error {
	return nil
}

func (its incomingTs) toInt() (int64, error) {
	var (
		tsI64 int64
		err   error
	)
	if tsI64, err = strconv.ParseInt(string(its), 10, 64); err != nil {
		return tsI64, err
	}
	return tsI64, nil
}
