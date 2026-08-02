package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	httpAddress := flag.String("http", ":8090", "HTTP listen address")
	tlsAddress := flag.String("tls", ":8443", "TLS listen address")
	certFile := flag.String("cert", "/certs/server.pem", "TLS certificate")
	keyFile := flag.String("key", "/certs/server-key.pem", "TLS private key")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	var mode atomic.Value
	mode.Store("healthy")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(writer http.ResponseWriter, _ *http.Request) {
		switch mode.Load().(string) {
		case "fail":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte("controlled failure"))
		case "slow":
			time.Sleep(3 * time.Second)
			_, _ = writer.Write([]byte("demo target healthy"))
		default:
			_, _ = writer.Write([]byte("demo target healthy"))
		}
	})
	mux.HandleFunc("GET /control", func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]string{"mode": mode.Load().(string)})
	})
	mux.HandleFunc("POST /control", func(writer http.ResponseWriter, request *http.Request) {
		selected := request.URL.Query().Get("mode")
		if selected != "healthy" && selected != "fail" && selected != "slow" {
			http.Error(writer, "mode must be healthy, fail, or slow", http.StatusBadRequest)
			return
		}
		mode.Store(selected)
		logger.Info("controlled mode changed", "mode", selected)
		_ = json.NewEncoder(writer).Encode(map[string]string{"mode": selected})
	})
	httpServer := &http.Server{Addr: *httpAddress, Handler: mux, ReadHeaderTimeout: 3 * time.Second}
	tlsServer := &http.Server{Addr: *tlsAddress, Handler: mux, ReadHeaderTimeout: 3 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		logger.Info("demo HTTP target started", "address", *httpAddress)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP target", "error", err)
			stop()
		}
	}()
	go func() {
		logger.Info("demo TLS target started", "address", *tlsAddress)
		if err := tlsServer.ListenAndServeTLS(*certFile, *keyFile); err != nil && err != http.ErrServerClosed {
			logger.Error("TLS target", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdown)
	_ = tlsServer.Shutdown(shutdown)
}
