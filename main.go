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

const (
	protocol   = "http"
	serverAddr = "127.0.0.1:8080"
	getPath    = "/retrieve"
	putPath    = "/update"
)

var (
	th         timestampHandler
	client     *http.Client
	httpServer *http.Server
)

func init() {
	initClient()
	initDataStore()
	initServer()
}

func main() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	// start the HTTP Server
	go startHTTPServer()

	// store and retrieve by Client
	makePutReq("12345678")
	makeGetReq()

	<-sigCh
	fmt.Println("shutting down server")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		_, err := fmt.Fprintf(os.Stderr, "error while shutting down httpServer: %s\n", err.Error())
		handleErrFunc(err)
	}
}

type timestampHandler interface {
	store(ts int64)
	get() time.Time
}

// data store
type dataStore struct {
	ts atomic.Value
}

func (ds *dataStore) store(ts int64) {
	if ds == nil {
		panic("writing to uninitialized dataStore")
	}
	ds.ts.Store(time.Unix(ts, 0))
}

func (ds *dataStore) get() time.Time {
	if ds == nil {
		panic("reading from uninitialized dataStore")
	}
	var ts time.Time
	val := ds.ts.Load()
	ts = val.(time.Time)
	return ts
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
	// 1 kB should be enough
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	timestamp = incomingTs(data)
	int64Ts, err := timestamp.toInt64()
	if err != nil {
		http.Error(w, "invalid timestamp in request body", http.StatusBadRequest)
		return
	}
	th.store(int64Ts)
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
func handleErrFunc(err error) {
	if err != nil {
		fmt.Println("could not write error message: " + err.Error())
	}
}

func makePutReq(ts string) {
	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s://%s%s", protocol, serverAddr, putPath), bytes.NewReader([]byte(ts)))
	if err != nil {
		_, err = fmt.Fprintf(os.Stderr, "error while creating request: %s\n", err.Error())
		handleErrFunc(err)
		return
	}
	req.Header.Set("Content-Type", "text/plain")
	rsp, err := client.Do(req)
	if err != nil {
		_, err = fmt.Fprintf(os.Stderr, "error while making PUT request: %s\n", err.Error())
		handleErrFunc(err)
		return
	}
	if rsp.StatusCode != http.StatusOK {
		_, err = fmt.Fprintf(os.Stderr, "recieved non 200 status code from server: %s\n", rsp.Status)
		handleErrFunc(err)
		if rsp.Body != nil {
			msg, err := io.ReadAll(rsp.Body)
			if err != nil {
				_, err = fmt.Fprintf(os.Stderr, "error while reading error response: %s\n", err.Error())
				handleErrFunc(err)
				return
			}
			defer rsp.Body.Close()
			_, err = fmt.Fprintf(os.Stderr, "error response: %s", string(msg))
			handleErrFunc(err)
		}
	}
	defer rsp.Body.Close()
}

func makeGetReq() {
	handleErrFunc := func(err error) {
		if err != nil {
			fmt.Println("could not write error message: " + err.Error())
		}
	}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s://%s%s", protocol, serverAddr, getPath), nil)
	if err != nil {
		_, err = fmt.Fprintf(os.Stderr, "error while creating request: %s\n", err.Error())
		handleErrFunc(err)
		return
	}
	rsp, err := client.Do(req)
	if err != nil {
		_, err = fmt.Fprintf(os.Stderr, "error while making get request: %s\n", err.Error())
		handleErrFunc(err)
		return
	}
	if rsp.StatusCode != http.StatusOK {
		_, err = fmt.Fprintf(os.Stderr, "recieved non 200 status code from server: %s\n", rsp.Status)
		handleErrFunc(err)
	}
	data, err := io.ReadAll(rsp.Body)
	if err != nil {
		_, err = fmt.Fprintf(os.Stderr, "error while reading response body: %s\n", err.Error())
		handleErrFunc(err)
		return
	}
	defer rsp.Body.Close()

	fmt.Fprintf(os.Stdout, "recieved timestamp from server: %s\n", string(data))
}

// helpers
func initDataStore() {
	th = &dataStore{}
	th.store(0)
}

func initClient() {
	client = &http.Client{
		Timeout: time.Second * 5,
	}
}

func initServer() {
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
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
}

func startHTTPServer() {
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Printf("error while listening: %s\n", err.Error())
		return
	}
}

type incomingTs string

func (its incomingTs) toInt64() (int64, error) {
	var (
		tsI64 int64
		err   error
	)
	if tsI64, err = strconv.ParseInt(string(its), 10, 64); err != nil {
		return tsI64, err
	}
	return tsI64, nil
}
