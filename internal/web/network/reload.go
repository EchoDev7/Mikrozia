package network

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"time"
)

// ReloadHTTP returns only after the replacement listener has bound with a validated TLS pair.
func ReloadHTTP(server **http.Server, listener *net.Listener, handler http.Handler, config *tls.Config, certPath, keyPath string) error {
	var pair tls.Certificate
	var err error
	secure := certPath != "" || keyPath != ""
	if secure {
		pair, err = tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return err
		}
	}
	if *listener == nil || *server == nil {
		return errors.New("listener is not initialized")
	}
	address := (*listener).Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := (*server).Shutdown(ctx); err != nil {
		return err
	}
	if err := (*listener).Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	raw, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return err
	}
	if config == nil {
		config = &tls.Config{}
	} else {
		config = config.Clone()
	}
	config.Certificates = []tls.Certificate{pair}
	next := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
	var nextListener net.Listener = raw
	if secure {
		nextListener = tls.NewListener(NewAutoHttpsListener(raw), config)
	}
	*server, *listener = next, nextListener
	go ServeHTTP(next, nextListener, "Reloaded TLS server")
	return nil
}
