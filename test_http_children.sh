#!/bin/bash
# Test script to prove whether Go net/http spawns ssl_client processes

echo "Building test binary with CGO_ENABLED=0..."
cd /tmp
cat > test_http.go << 'EOF'
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	pid := os.Getpid()
	fmt.Printf("Test process PID: %d\n", pid)
	fmt.Printf("Making HTTPS request to https://httpbin.org/get\n")
	
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://httpbin.org/get")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	
	fmt.Printf("HTTP Status: %d\n", resp.StatusCode)
	fmt.Printf("\nSleeping for 10 seconds - check for child processes...\n")
	fmt.Printf("Run: ps -ef | grep %d\n", pid)
	
	time.Sleep(10 * time.Second)
	fmt.Printf("Done - no ssl_client children should exist.\n")
}
EOF

CGO_ENABLED=0 go build -o test_http test_http.go
./test_http &
TEST_PID=$!

sleep 2
echo ""
echo "=== Checking for ssl_client children of PID $TEST_PID ==="
ps -ef | grep $TEST_PID | grep -v grep
echo ""
echo "=== Checking for any ssl_client processes ==="
ps -ef | grep ssl_client | grep -v grep || echo "No ssl_client processes found"
echo ""

wait $TEST_PID
echo "Test complete."
