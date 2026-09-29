package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pbuzdygan/filedeck/internal/api"
	"github.com/pbuzdygan/filedeck/internal/core"
	"github.com/pbuzdygan/filedeck/internal/identity"
	"golang.org/x/term"
)

type serverOptions struct {
	listen, origin, cert, key, proxy string
	insecure, selfSigned             bool
	secretKey                        []byte
}

// secretKey reads the key protecting two-factor secrets from
// FILEDECK_SECRET_KEY or the file named by FILEDECK_SECRET_KEY_FILE (e.g. a
// Docker secret): 32 bytes in base64, as printed by "openssl rand -base64 32".
// It comes only from the environment, never from a flag visible in ps.
func secretKey() ([]byte, error) {
	value := os.Getenv("FILEDECK_SECRET_KEY")
	file := os.Getenv("FILEDECK_SECRET_KEY_FILE")
	if value != "" && file != "" {
		return nil, errors.New("set FILEDECK_SECRET_KEY or FILEDECK_SECRET_KEY_FILE, not both")
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("FILEDECK_SECRET_KEY_FILE: %w", err)
		}
		value = string(data)
	}
	if value == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(key) != identity.SecretKeySize {
		return nil, errors.New("FILEDECK_SECRET_KEY must be 32 bytes in base64 - generate one with: openssl rand -base64 32")
	}
	return key, nil
}

func readSecret() (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Password (minimum 12 characters): ")
		b, e := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if e != nil {
			return "", e
		}
		if len(b) > 1024 {
			return "", identity.ErrInvalid
		}
		return string(b), nil
	}
	b, e := io.ReadAll(io.LimitReader(os.Stdin, 1026))
	if e != nil {
		return "", e
	}
	if len(b) > 1025 {
		return "", identity.ErrInvalid
	}
	// Accept one trailing line ending from a pipe; do not trim password spaces.
	p := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
	if strings.ContainsAny(p, "\r\n") {
		return "", identity.ErrInvalid
	}
	return p, nil
}
func accountCommand(state string, rest []string) (err error) {
	store, err := identity.Open(state)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	secret := ""
	if rest[0] != "reset-2fa" {
		if secret, err = readSecret(); err != nil {
			return err
		}
	}
	if rest[0] == "bootstrap" {
		_, err = store.Bootstrap(context.Background(), rest[1], secret)
		return err
	}
	users, err := store.Users()
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.Username == rest[1] && rest[0] == "reset-2fa" {
			return store.DisableTOTP(u.ID, "")
		}
		if u.Username == rest[1] {
			return store.ResetPassword(context.Background(), u.ID, secret)
		}
	}
	return identity.ErrNotFound
}
func serve(files *core.Service, state string, limits core.Limits, o serverOptions) (err error) {
	host, _, err := net.SplitHostPort(o.listen)
	if err != nil {
		return err
	}
	if o.insecure {
		ip, e := netip.ParseAddr(host)
		if e != nil || !ip.IsLoopback() {
			return errors.New("insecure-local requires binding a loopback IP")
		}
	}
	if (o.cert == "") != (o.key == "") {
		return errors.New("both TLS certificate and key are required")
	}
	if o.selfSigned && (o.cert != "" || o.proxy != "") {
		return errors.New("tls-self-signed cannot be combined with tls-cert or proxy-cidr")
	}
	if !o.insecure && o.cert == "" && o.proxy == "" && !o.selfSigned {
		return errors.New("configure TLS (tls-cert/tls-key or tls-self-signed) or an explicit trusted proxy CIDR")
	}
	if o.insecure && (o.cert != "" || o.selfSigned) {
		return errors.New("insecure-local cannot use TLS")
	}
	store, err := identity.Open(state)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	unprotected, err := store.Protect(o.secretKey)
	if err != nil {
		return fmt.Errorf("FILEDECK_SECRET_KEY: %w", err)
	}
	if o.secretKey == nil {
		fmt.Fprintf(os.Stderr, "Two-factor secrets are not encrypted (%d accounts use two-factor authentication): set FILEDECK_SECRET_KEY, see README.\n", unprotected)
	} else {
		fmt.Fprintln(os.Stderr, "Two-factor secrets are encrypted with FILEDECK_SECRET_KEY.")
	}
	handler, err := api.New(store, files, api.Config{Origin: o.origin, InsecureLocal: o.insecure, ProxyCIDR: o.proxy, Limits: limits, AllowSetup: true,
		Log: slog.New(slog.NewTextHandler(os.Stderr, nil))})
	if err != nil {
		return err
	}
	server := &http.Server{Addr: o.listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: api.RequestTimeout + 5*time.Second, WriteTimeout: api.RequestTimeout + 5*time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	useTLS := o.cert != ""
	if o.selfSigned {
		cert, sum, e := selfSigned(state, o.origin)
		if e != nil {
			return e
		}
		server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		useTLS = true
		fmt.Fprintln(os.Stderr, "Self-signed certificate SHA-256 fingerprint:", sum)
	}
	listener, err := net.Listen("tcp", o.listen)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() {
		if useTLS {
			done <- server.ServeTLS(listener, o.cert, o.key)
		} else {
			done <- server.Serve(listener)
		}
	}()
	fmt.Fprintln(os.Stderr, "Filedeck listening on", listener.Addr(), "- open", o.origin)
	if o.proxy != "" {
		fmt.Fprintf(os.Stderr, "Behind a reverse proxy: plain HTTP on %s, accepted only from %s; the proxy must pass the Host header %q.\n", o.listen, o.proxy, strings.TrimPrefix(strings.TrimPrefix(o.origin, "https://"), "http://"))
	}
	for _, sp := range files.Spaces() {
		mode := "read-write"
		if sp.ReadOnly {
			mode = "read-only"
		}
		fmt.Fprintf(os.Stderr, "Space %q: %s\n", sp.Name, mode)
	}
	if code := handler.SetupCode(); code != "" {
		fmt.Fprintf(os.Stderr, "No administrator yet. Open %s and enter the one-time setup code: %s\n(The code changes on every start and stops working once the administrator exists.)\n", o.origin, code)
	}
	maintenanceDone := make(chan struct{})
	go func() {
		defer close(maintenanceDone)
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if e := files.Expire(); e != nil {
					log.Print("upload expiration failed")
				}
			}
		}
	}()
	select {
	case err = <-done:
		stop()
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), api.RequestTimeout+5*time.Second)
		err = server.Shutdown(shutdown)
		cancel()
		if err != nil {
			_ = server.Close()
		}
		serveErr := <-done
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			err = errors.Join(err, serveErr)
		}
	}
	stop()
	<-maintenanceDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
