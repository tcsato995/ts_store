package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	ds         timestampHandler
	client     *http.Client
	httpServer *http.Server
)

func init() {
	ds = &dataStore{}
	client = &http.Client{
		Timeout: time.Minute * 1,
	}
	initServer()
}

func main() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	// start the HTTP Server
	go startHTTPServer()
	// store and retrieve by Client
	makePutReq()
	makeGetReq()

	<-sigCh
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error while shutting down httpServer: %s", err.Error())
	}
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
	ds.blockWhileWrite()
	return ds.ts
}

func (ds *dataStore) blockWhileWrite() {
	var readable uint32
	for atomic.LoadUint32(&ds.writeFlag) != readable {
	}
}

// HTTP handlers
func update(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Content-Type") != "text/plain" {
		http.Error(w, "only text/plain content-type is allowed", http.StatusBadRequest)
		return
	}
	if r.Body == nil {
		http.Error(w, "request body missing", http.StatusBadRequest)
		return
	}
	var (
		timestamp incomingTs
		err       error
	)
	// kB seems like enough
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		fmt.Printf("error while reading the request body: %s", err.Error())
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	timestamp = incomingTs(data)
	intTs, err := timestamp.toInt()
	if err != nil {
		http.Error(w, "invalid timestamp in request body", http.StatusBadRequest)
		return
	}
	ds.store(intTs)
	w.WriteHeader(http.StatusOK)
}

func retrieve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(strconv.Itoa(int(ds.get().Unix()))))
}

// client code
func makePutReq() {
	req, err := http.NewRequest(http.MethodPut, "http://127.0.0.1:8080/update", bytes.NewReader([]byte("12345678")))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error while creating request: %s", err.Error())
		return
	}
	req.Header.Set("Content-Type", "text/plain")
	rsp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error while making PUT request: %s", err.Error())
		return
	}
	defer rsp.Body.Close()
}

func makeGetReq() {
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8080/retrieve", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error while creating request: %s", err.Error())
		return
	}
	rsp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error while making get request: %s", err.Error())
		return
	}
	defer rsp.Body.Close()
	data, err := io.ReadAll(rsp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error while reading response body: %s", err.Error())
		return
	}
	fmt.Fprintf(os.Stdout, "recieved timestamp from server: %s", string(data))
}

// helpers
func initServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/update", update)
	mux.HandleFunc("/retrieve", retrieve)
	httpServer = &http.Server{
		Handler: mux,
		Addr:    "127.0.0.1:8080",
	}
}

func startHTTPServer() {
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Printf("error while listening: %s", err.Error())
		return
	}
}

type incomingTs string

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
