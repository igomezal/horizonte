package main

import (
	"fmt"
	"net"
	"net/http"
	"time"
)

// healthcheck asks the server listening on addr for /healthz. It runs as a
// separate process (`server -healthcheck`), so container images without a
// shell or curl can still have a health check.
func healthcheck(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1" // listening on all interfaces: ask over loopback
	}
	client := http.Client{Timeout: 3 * time.Second}
	res, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	return nil
}
