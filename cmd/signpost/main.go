// Signpost is the front page for a goatnet's administrators: links to every
// surface on the node, with read-only health and usage stats beside them.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/philosoma/signpost/internal/collect"
	"github.com/philosoma/signpost/internal/config"
	"github.com/philosoma/signpost/internal/server"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "/etc/signpost/config.json", "path to the config file")
	listen := flag.String("listen", ":8080", "address to listen on")
	healthcheck := flag.Bool("healthcheck", false, "check a running instance and exit (the image has no shell or curl)")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthcheck(*listen))
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(*configPath, *listen, log); err != nil {
		log.Error("signpost stopped", "error", err)
		os.Exit(1)
	}
}

func run(configPath, listen string, log *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	collector := collect.New(cfg, config.SecretsFromEnv(), log)
	go collector.Run(ctx)

	handler, err := server.New(cfg, collector, version, log)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("signpost listening", "listen", listen, "version", version)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func runHealthcheck(listen string) int {
	host := listen
	if len(host) > 0 && host[0] == ':' {
		host = "127.0.0.1" + host
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + host + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthz:", resp.Status)
		return 1
	}
	return 0
}
