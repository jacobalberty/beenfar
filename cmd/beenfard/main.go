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

	"github.com/jacobalberty/beenfar/adapters/unifi"
	"github.com/jacobalberty/beenfar/internal/adapterhost"
	"github.com/jacobalberty/beenfar/internal/auth"
	"github.com/jacobalberty/beenfar/internal/core"
	"github.com/jacobalberty/beenfar/internal/server"
	"github.com/jacobalberty/beenfar/internal/store"
	"github.com/jacobalberty/beenfar/internal/store/otelstore"
	"github.com/jacobalberty/beenfar/internal/store/sqlite"
	"github.com/jacobalberty/beenfar/internal/telemetry"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var err error
	if len(os.Args) > 1 && os.Args[1] == "admin" {
		err = runAdmin(os.Args[2:], os.Stdin, os.Stdout)
	} else {
		err = run()
	}
	if err != nil {
		slog.ErrorContext(context.Background(), "beenfard exited", slog.Any("error", err))
		os.Exit(1)
	}
}

type config struct {
	listen    string
	db        string
	logFormat string
	logLevel  slog.Level
	adminUser string
	auth      *authConfig

	unifiInterval int  // seconds between informs of a UniFi device
	unifiGCM      bool // adopt UniFi devices onto AES-GCM
}

func parseFlags(args []string) (config, error) {
	var cfg config
	fs := flag.NewFlagSet("beenfard", flag.ContinueOnError)
	fs.StringVar(&cfg.listen, "listen", envOr("BEENFAR_LISTEN", ":8080"), "listen address (env BEENFAR_LISTEN)")
	fs.StringVar(&cfg.db, "db", envOr("BEENFAR_DB", "sqlite://beenfar.db"), "database URL, e.g. sqlite:///var/lib/beenfar/beenfar.db (env BEENFAR_DB)")
	fs.StringVar(&cfg.logFormat, "log-format", envOr("BEENFAR_LOG_FORMAT", "json"), "log format: json or text (env BEENFAR_LOG_FORMAT)")
	level := fs.String("log-level", envOr("BEENFAR_LOG_LEVEL", "info"), "log level: debug, info, warn, error (env BEENFAR_LOG_LEVEL)")
	fs.StringVar(&cfg.adminUser, "admin-user", envOr("BEENFAR_ADMIN_USER", "admin"),
		"username of the admin created on first start when BEENFAR_ADMIN_PASSWORD is set (env BEENFAR_ADMIN_USER)")
	cfg.auth = registerAuthFlags(fs)
	fs.IntVar(&cfg.unifiInterval, "unifi-inform-interval", cfg.auth.envInt("BEENFAR_UNIFI_INFORM_INTERVAL", unifi.DefaultInterval),
		"seconds between informs of a UniFi device (env BEENFAR_UNIFI_INFORM_INTERVAL)")
	fs.BoolVar(&cfg.unifiGCM, "unifi-gcm", cfg.auth.envBool("BEENFAR_UNIFI_GCM", false),
		"switch UniFi devices to AES-GCM when adopting them (env BEENFAR_UNIFI_GCM)")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if err := cfg.logLevel.UnmarshalText([]byte(*level)); err != nil {
		return cfg, fmt.Errorf("log level: %w", err)
	}
	if cfg.unifiInterval < 1 {
		return cfg, fmt.Errorf("unifi inform interval %d: want at least 1 second", cfg.unifiInterval)
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

	authSvc := auth.New(ost, cfg.auth.serviceOptions()...)
	if err := bootstrapAdmin(ctx, authSvc, cfg.adminUser, os.Getenv("BEENFAR_ADMIN_PASSWORD")); err != nil {
		return err
	}
	authn, methods, err := cfg.auth.build(authSvc, os.Getenv)
	if err != nil {
		return fmt.Errorf("auth configuration: %w", err)
	}
	adapters, err := adapterhost.NewRegistry(adapterhost.Options{Store: ost, Meter: tel.Meter(), Propagator: tel.TextMapPropagator()})
	if err != nil {
		return err
	}
	if err := adapters.AddBuiltin(unifi.New(unifi.Options{
		Tracer:   tel.Tracer(),
		Interval: cfg.unifiInterval,
		UseGCM:   cfg.unifiGCM,
	})); err != nil {
		return err
	}
	adapterCtx, stopAdapters := context.WithCancel(ctx)
	adapters.Start(adapterCtx)
	defer func() { stopAdapters(); adapters.Wait() }() // also on startup errors
	handler, err := server.New(server.Options{
		Store:         ost,
		Telemetry:     tel,
		Auth:          authSvc,
		Authenticator: authn,
		AuthMethods:   methods,
		Core:          core.New(ost, adapters),
		AdapterHost:   adapterhost.NewService(adapters, ost),
		AdapterHTTP:   adapters.HTTPHandlers(),
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		Protocols:         new(http.Protocols),
	}
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true) // h2c, for gRPC clients

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

// bootstrapAdmin creates the first admin from BEENFAR_ADMIN_PASSWORD when
// the database has no users, and otherwise explains how to create one.
func bootstrapAdmin(ctx context.Context, svc *auth.Service, username, password string) error {
	if password == "" {
		n, err := svc.CountUsers(ctx)
		if err != nil {
			return err
		}
		if n == 0 {
			slog.WarnContext(ctx, "no users exist; set BEENFAR_ADMIN_PASSWORD or run `beenfard admin create` to create an admin")
		}
		return nil
	}
	created, err := svc.Bootstrap(ctx, username, password)
	if err != nil {
		return fmt.Errorf("creating initial admin: %w", err)
	}
	if created {
		slog.InfoContext(ctx, "created initial admin user", slog.String("username", username))
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
