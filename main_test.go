package main

import (
	"testing"
	"time"
)

func TestInit(t *testing.T) {
	if client == nil {
		t.Error("http client is still nil after init")
	}
	if client.Timeout != 5*time.Second {
		t.Errorf("client has unexpected timeout: %d", client.Timeout)
	}
	if httpServer == nil {
		t.Error("httpServer is nil after init")
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
