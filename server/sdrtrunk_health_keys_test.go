package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDeletedKeyIsRemovedFromSdrHealth(t *testing.T) {
	serverConns := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := sdrtrunkUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		serverConns <- conn
	}))
	defer server.Close()

	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	controller := &Controller{Apikeys: NewApikeys(), Logs: NewLogs()}
	controller.Apikeys.List = []*Apikey{{Id: 51, Ident: "Catlettsburg KY", Key: "kept", Purpose: "site"}}
	controller.sdrtrunkHealth = map[uint64]*SdrtrunkHealthRecord{
		51: {ApiKeyId: 51, Reporting: true},
		52: {ApiKeyId: 52, Reporting: true},
	}
	controller.sdrtrunkHub().register(52, &sdrtrunkSession{conn: <-serverConns})

	controller.forgetDeletedSdrtrunkKeys()

	if _, ok := controller.sdrtrunkHealth[52]; ok {
		t.Fatal("a deleted key must not stay in SDR Health")
	}
	if _, ok := controller.sdrtrunkHealth[51]; !ok {
		t.Fatal("an existing key must stay in SDR Health")
	}
	if controller.sdrtrunkHub().connected(52) {
		t.Fatal("SDRTrunk connected with a deleted key must be disconnected")
	}
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := client.ReadMessage(); err == nil {
		t.Fatal("the deleted key's connection should be closed")
	}
}

func TestSdrHealthKeptWhenKeysAreNotLoaded(t *testing.T) {
	controller := &Controller{Apikeys: NewApikeys(), Logs: NewLogs()}
	controller.sdrtrunkHealth = map[uint64]*SdrtrunkHealthRecord{51: {ApiKeyId: 51}}

	controller.forgetDeletedSdrtrunkKeys()

	if _, ok := controller.sdrtrunkHealth[51]; !ok {
		t.Fatal("records must not be removed when no API keys are loaded")
	}
}
