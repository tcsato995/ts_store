package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestInit(t *testing.T) {
	if client == nil {
		t.Error("http client is still nil after init")
	}
	if client.Timeout != defaultTimeout {
		t.Errorf("client has unexpected timeout: %d", client.Timeout)
	}
	if httpServer == nil {
		t.Error("httpServer is nil after init")
	}
	if httpServer.Handler == nil {
		t.Error("httpServer has nil handler after init")
	}
	if httpServer.Addr != serverAddr {
		t.Errorf("http server has a different address than expected: %s", httpServer.Addr)
	}
	if th == nil {
		t.Error("timestampHandler is nil even after init")
	}
	if th.get().Unix() != 0 {
		t.Errorf("initial timestamp stored is not 0: %d", th.get().Unix())
	}
}

func TestInitServer(t *testing.T) {
	if httpServer.ReadTimeout != defaultTimeout && httpServer.WriteTimeout != defaultTimeout {
		t.Error("httpServer timeout is not as expected")
	}
	newTimeout := 10 * time.Second
	initServer(newTimeout)
	if httpServer.ReadTimeout != newTimeout && httpServer.WriteTimeout != newTimeout {
		t.Error("httpServer timeout is not updated")
	}
}

func TestInitClient(t *testing.T) {
	if client.Timeout != defaultTimeout {
		t.Error("client timeout is not as expected")
	}
	newTimeout := 10 * time.Second
	initClient(newTimeout)
	if client.Timeout != newTimeout {
		t.Error("client timeout is not updated")
	}
}

func TestTimestamp(t *testing.T) {
	tests := []struct {
		description string
		inputTs     timestamp
		expectedTs  any
	}{
		{"valid1", "1", int64(1)},
		{"invalid1", "-1", "timestamp supplied is negative"},
		{"valid2", "1234567", int64(1234567)},
		{"valid3", timestamp(strconv.FormatInt(math.MaxInt64, 10)), int64(math.MaxInt64)},
		{"invalid4", "notvalidts", "invalid timestamp"},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			val, err := test.inputTs.toUnixTime()
			if err != nil {
				if err.Error() != test.expectedTs {
					t.Errorf("unexpected error: %s", err.Error())
				}
				return
			}
			if val.Unix() != test.expectedTs.(int64) {
				t.Errorf("unexpected value: %d", val.Unix())
			}
		})
	}
}

func TestTimestampHandler(t *testing.T) {
	tests := []struct {
		description string
		inputTs     time.Time
		expectedTs  time.Time
	}{
		{"1", time.Unix(1, 0), time.Unix(1, 0)},
		{"100", time.Unix(100, 0), time.Unix(100, 0)},
		{"9", time.Unix(9, 0), time.Unix(9, 0)},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			th.store(test.inputTs)
			if th.get() != test.expectedTs {
				t.Errorf("expected: %d, got: %d", test.inputTs.Unix(), test.expectedTs.Unix())
			}
		})
	}
}

func TestForRace(t *testing.T) {
	defer func() {
		th.store(time.Unix(0, 0))
	}()

	// while there is no expectations for the outcomes, as it is hard to predict what the scheduler will do
	// running with the -race flag should error if there is any race condition here
	var wg sync.WaitGroup
	for i := range 15 {
		wg.Add(1)
		if i%2 == 0 {
			go func(ts int64) {
				th.store(time.Unix(ts, 0))
				wg.Done()
			}(int64(i))
		} else {
			go func() {
				th.get()
				wg.Done()
			}()
		}
	}
	wg.Wait()
}

func TestLog(t *testing.T) {
	buf := bytes.NewBuffer([]byte{})
	log(buf, "testing log functionality")
	if buf.String() != "testing log functionality" {
		t.Errorf("buf is not as expected: %s", buf.String())
	}
	buf.Reset()
	log(buf, "with format string: %s", "OK")
	if buf.String() != "with format string: OK" {
		t.Errorf("buf is not as expected: %s", buf.String())
	}
}

func TestHttpServer(t *testing.T) {
	go func() {
		startHTTPServer()
	}()
	defer stopHttpServer()

	time.Sleep(time.Second * 2)
	makePutReq("200")
	if makeGetReq() != "200" {
		t.Fatalf("put request was not successful")
	}
	makePutReq("1000")
	if makeGetReq() != "1000" {
		t.Fatalf("put request was not successful")
	}
	makePutReq("invalid")
	if makeGetReq() != "1000" {
		t.Fatalf("response is not what is expected")
	}
}

func TestRetrieveHandler(t *testing.T) {
	th.store(time.Unix(10, 0))

	th.store(time.Unix(10, 0))
	type tc struct {
		description        string
		method             string
		expectedErr        error
		expectedStatusCode int
		expectedTs         string
		setupValue         time.Time
	}
	testCases := []tc{
		{
			description:        "OK",
			method:             http.MethodGet,
			expectedErr:        nil,
			expectedStatusCode: http.StatusOK,
			setupValue:         time.Unix(10, 0),
			expectedTs:         "10",
		},
		{
			description:        "OK 2",
			method:             http.MethodGet,
			expectedErr:        nil,
			expectedStatusCode: http.StatusOK,
			setupValue:         time.Unix(100, 0),
			expectedTs:         "100",
		},
		{
			description:        "bad method",
			method:             http.MethodPut,
			expectedErr:        errors.New("method not allowed\n"),
			expectedStatusCode: http.StatusMethodNotAllowed,
			setupValue:         time.Unix(100, 0),
			expectedTs:         "",
		},
	}
	for _, test := range testCases {
		t.Run(test.description, func(t *testing.T) {
			th.store(test.setupValue)

			req := httptest.NewRequest(test.method, fmt.Sprintf("%s://%s%s", protocol, serverAddr, getPath), nil)
			w := httptest.NewRecorder()
			retrieve(w, req)
			res := w.Result()
			if res.StatusCode != test.expectedStatusCode {
				t.Errorf("expected status code to be %d, got: %d", test.expectedStatusCode, res.StatusCode)
			}
			if res.Body == nil {
				t.Error("response body is nil")
			}
			data, err := io.ReadAll(res.Body)
			if err != nil {
				t.Errorf("could not read response body: %v", err)
			}
			defer res.Body.Close()
			if test.expectedErr != nil {
				if string(data) != test.expectedErr.Error() {
					t.Errorf("expected response to be: %s, got: %s", test.expectedErr.Error(), string(data))
				}
			} else {
				if string(data) != test.expectedTs {
					t.Errorf("expected %s, got %s", test.expectedTs, string(data))
				}
			}
		})
	}
}

func TestUpdateHandler(t *testing.T) {
	th.store(time.Unix(10, 0))
	type tc struct {
		description        string
		contentType        string
		method             string
		body               io.Reader
		expectedErr        error
		expectedStatusCode int
	}
	testCases := []tc{
		{
			description:        "OK",
			contentType:        "text/plain",
			method:             http.MethodPut,
			body:               bytes.NewReader([]byte("1234567")),
			expectedErr:        nil,
			expectedStatusCode: http.StatusOK,
		},
		{
			description:        "invalid content type",
			contentType:        "application/json",
			method:             http.MethodPut,
			body:               bytes.NewReader([]byte("1234567")),
			expectedErr:        errors.New("only text/plain content-type is allowed\n"),
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			description:        "invalid method",
			contentType:        "text/plain",
			method:             http.MethodPost,
			body:               bytes.NewReader([]byte("1234567")),
			expectedErr:        errors.New("method not allowed\n"),
			expectedStatusCode: http.StatusMethodNotAllowed,
		},
		{
			description:        "invalid method and content type",
			contentType:        "invalid",
			method:             http.MethodPatch,
			body:               bytes.NewReader([]byte("1234567")),
			expectedErr:        errors.New("method not allowed\n"),
			expectedStatusCode: http.StatusMethodNotAllowed,
		},
		{
			description:        "invalid timestamp",
			contentType:        "text/plain",
			method:             http.MethodPut,
			body:               bytes.NewReader([]byte("-1")),
			expectedErr:        errors.New("invalid timestamp in request body\n"),
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			description:        "request body too large",
			contentType:        "text/plain",
			method:             http.MethodPut,
			body:               bytes.NewReader(make([]byte, maxReqBytes+1)),
			expectedErr:        errors.New("invalid request body\n"),
			expectedStatusCode: http.StatusBadRequest,
		},
	}

	for _, test := range testCases {
		t.Run(test.description, func(t *testing.T) {
			req := httptest.NewRequest(test.method, fmt.Sprintf("%s://%s%s", protocol, serverAddr, putPath), test.body)
			req.Header.Set("Content-Type", test.contentType)
			w := httptest.NewRecorder()
			update(w, req)
			res := w.Result()
			if res.StatusCode != test.expectedStatusCode {
				t.Errorf("expected status code to be %d, got: %d", test.expectedStatusCode, res.StatusCode)
			}
			if res.Body == nil {
				t.Error("response body is nil")
			}
			data, err := io.ReadAll(res.Body)
			if err != nil {
				t.Errorf("could not read response body: %v", err)
			}
			defer res.Body.Close()
			if test.expectedErr != nil {
				if string(data) != test.expectedErr.Error() {
					t.Errorf("expected response to be: %s, got: %s", test.expectedErr.Error(), string(data))
				}
			}
		})
	}
}
