package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// healthcheck asks the local server for /healthz. It is meant for container
// health checks: it connects to the loopback address of the listen port and
// does not verify the certificate (the connection never leaves the container).
func healthcheck(listen, origin string, plain bool) error {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return err
	}
	scheme := "https"
	if plain {
		scheme = "http"
	}
	host := "localhost"
	if u, e := url.Parse(origin); e == nil && u.Host != "" {
		host = u.Host
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // loopback-only liveness probe
		Proxy:           nil,
	}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect") }}
	req, err := http.NewRequest("GET", scheme+"://"+net.JoinHostPort("127.0.0.1", port)+"/healthz", nil)
	if err != nil {
		return err
	}
	req.Host = host
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("unhealthy: HTTP %d", res.StatusCode)
	}
	return nil
}
