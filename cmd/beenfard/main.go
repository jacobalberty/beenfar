// Command beenfard is the beenfar network controller daemon.
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
	"strings"
	"syscall"
	"time"

	"github.com/jacobalberty/beenfar/internal/server"
	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/store/otelstore"
	"github.com/jacobalberty/beenfar/internal/store/sqlite"
	"github.com/jacobalberty/beenfar/internal/telemetry"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.ErrorContext(context.Background(), "beenfard exited", slog.Any("error", err))
		os.Exit(1)
	}
}

type config struct {
	listen    string
	db        string
	logFormat string
	logLevel  slog.Level
}

func parseFlags(args []string) (config, error) {
	var cfg config
	fs := flag.NewFlagSet("beenfard", flag.ContinueOnError)
	fs.StringVar(&cfg.listen, "listen", envOr("BEENFAR_LISTEN", ":8080"), "listen address (env BEENFAR_LISTEN)")
	fs.StringVar(&cfg.db, "db", envOr("BEENFAR_DB", "sqlite://beenfar.db"), "database URL, e.g. sqlite:///var/lib/beenfar/beenfar.db (env BEENFAR_DB)")
	fs.StringVar(&cfg.logFormat, "log-format", envOr("BEENFAR_LOG_FORMAT", "json"), "log format: json or text (env BEENFAR_LOG_FORMAT)")
	level := fs.String("log-level", envOr("BEENFAR_LOG_LEVEL", "info"), "log level: debug, info, warn, error (env BEENFAR_LOG_LEVEL)")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if err := cfg.logLevel.UnmarshalText([]byte(*level)); err != nil {
		return cfg, fmt.Errorf("log level: %w", err)
	}
	if cfg.logFormat != "json" && cfg.logFormat != "text" {
		return cfg, fmt.Errorf("log format %q: want json or text", cfg.logFormat)
	}
	return cfg, nil
}

func run() error {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tel, err := telemetry.Setup(ctx, telemetry.Config{ServiceName: "beenfard", ServiceVersion: version})
	if err != nil {
		return fmt.Errorf("telemetry: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tel.Shutdown(shutdownCtx); err != nil {
			slog.WarnContext(shutdownCtx, "telemetry shutdown", slog.Any("error", err))
		}
	}()
	logger := tel.Logger(telemetry.LogConfig{Writer: os.Stderr, Format: cfg.logFormat, Level: cfg.logLevel})
	slog.SetDefault(logger)

	st, system, err := openStore(ctx, cfg.db)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	ost, err := otelstore.New(st, system, tel)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.listen,
		Handler:           server.New(server.Options{Store: ost, Telemetry: tel}),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		Protocols:         new(http.Protocols),
	}
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true) // h2c, for gRPC clients from phase 2 on

	errCh := make(chan error, 1)
	go func() {
		slog.InfoContext(ctx, "listening", slog.String("addr", cfg.listen), slog.String("version", version))
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	slog.InfoContext(ctx, "shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// openStore opens the database named by a URL and returns it with its
// dialect name.
func openStore(ctx context.Context, url string) (store.Store, string, error) {
	if path, ok := strings.CutPrefix(url, "sqlite://"); ok {
		st, err := sqlite.Open(ctx, path)
		return st, "sqlite", err
	}
	return nil, "", fmt.Errorf("unsupported database URL %q (supported: sqlite://<path>)", url)
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
