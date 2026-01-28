package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	logger "log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	protocol       = "http"
	serverAddr     = ":8080"
	getPath        = "/retrieve"
	putPath        = "/update"
	defaultTimeout = 5 * time.Second
	maxReqBytes    = 1024 // 1 kB should be enough
)

var (
	th         timestampHandler
	client     *http.Client
	httpServer *http.Server
)

func init() {
	initClient(defaultTimeout)
	initServer(defaultTimeout)
	initDataStore()
}

func main() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	// start the HTTP Server
	go startHTTPServer()

	// store and retrieve by Client
	makePutReq("123456789")
	makeGetReq()

	<-sigCh
	stopHttpServer()
}

type timestampHandler interface {
	store(ts time.Time)
	get() time.Time
}

// data store
type dataStore struct {
	ts atomic.Value
}

func (ds *dataStore) store(ts time.Time) {
	if ds == nil {
		panic("writing to uninitialized dataStore")
	}
	ds.ts.Store(ts)
}

func (ds *dataStore) get() time.Time {
	if ds == nil {
		panic("reading from uninitialized dataStore")
	}
	val := ds.ts.Load()
	return val.(time.Time)
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
		ts  timestamp
		err error
	)
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxReqBytes))

	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		log(os.Stderr, "error while reading request body: %s", err.Error())
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ts = timestamp(data)
	unixTime, err := ts.toUnixTime()
	if err != nil {
		log(os.Stderr, "could not convert data to timestamp: %s", err.Error())
		http.Error(w, "invalid timestamp in request body", http.StatusBadRequest)
		return
	}
	th.store(unixTime)
	w.WriteHeader(http.StatusOK)
}

func retrieve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(strconv.FormatInt(th.get().Unix(), 10)))
}

// client code
func makePutReq(ts string) {
	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s://%s%s", protocol, serverAddr, putPath), bytes.NewReader([]byte(ts)))
	if err != nil {
		log(os.Stderr, "error while creating request: %s\n", err.Error())
		return
	}
	req.Header.Set("Content-Type", "text/plain")
	rsp, err := client.Do(req)
	if err != nil {
		log(os.Stderr, "error while making PUT request: %s\n", err.Error())
		return
	}
	if rsp.StatusCode != http.StatusOK {
		log(os.Stderr, "recieved non 200 status code from server: %s\n", rsp.Status)
		if rsp.Body != nil {
			msg, err := io.ReadAll(rsp.Body)
			if err != nil {
				log(os.Stderr, "error while reading error response: %s\n", err.Error())
				return
			}
			defer rsp.Body.Close()
			log(os.Stderr, "error response: %s", string(msg))
		}
	}
	defer rsp.Body.Close()
}

func makeGetReq() string {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s://%s%s", protocol, serverAddr, getPath), nil)
	if err != nil {
		log(os.Stderr, "error while creating request: %s\n", err.Error())
		return ""
	}
	rsp, err := client.Do(req)
	if err != nil {
		log(os.Stderr, "error while making get request: %s\n", err.Error())
		return ""
	}
	if rsp.StatusCode != http.StatusOK {
		log(os.Stderr, "recieved non 200 status code from server: %s\n", rsp.Status)
	}
	defer rsp.Body.Close()
	data, err := io.ReadAll(rsp.Body)
	if err != nil {
		log(os.Stderr, "error while reading response body: %s\n", err.Error())
		return ""
	}
	log(os.Stdout, "recieved timestamp from server: %s\n", string(data))
	return string(data)
}

// helpers
func log(w io.Writer, format string, a ...any) {
	_, err := fmt.Fprintf(w, format, a...)
	if err != nil {
		fmt.Println("could not write error message: " + err.Error())
	}
}

func initDataStore() {
	th = &dataStore{}
	th.store(time.Unix(0, 0))
}

func initClient(timeout time.Duration) {
	client = &http.Client{
		Timeout: timeout,
	}
}

func initServer(timeout time.Duration) {
	routes := map[string]http.HandlerFunc{
		putPath: update,
		getPath: retrieve,
	}
	mux := http.NewServeMux()
	for path, handler := range routes {
		mux.HandleFunc(path, handler)
	}
	httpServer = &http.Server{
		Handler:      mux,
		Addr:         serverAddr,
		ReadTimeout:  timeout,
		WriteTimeout: timeout,
	}
}

func startHTTPServer() {
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("error while listening: %s\n", err.Error())
		return
	}
}

func stopHttpServer() {
	log(os.Stdout, "shutting down server\n")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log(os.Stderr, "error while shutting down httpServer: %s\n", err.Error())
	}
}

type timestamp string

func (ts timestamp) toInt64() (int64, error) {
	var (
		tsI64 int64
		err   error
	)
	if tsI64, err = strconv.ParseInt(string(ts), 10, 64); err != nil {
		return tsI64, errors.New("invalid timestamp")
	}
	return tsI64, nil
}

func (ts timestamp) toUnixTime() (time.Time, error) {
	tsI64, err := ts.toInt64()
	if err != nil {
		return time.Time{}, err
	}
	if tsI64 < 0 {
		return time.Time{}, errors.New("timestamp supplied is negative")
	}
	return time.Unix(tsI64, 0), nil
}
