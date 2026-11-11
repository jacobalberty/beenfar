package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jacobalberty/beenfar/internal/auth"
)

const adminUsage = `usage:
  beenfard admin create [-db URL] [-username NAME] [-admin=true]
      Create a user. The password is read from BEENFAR_ADMIN_PASSWORD, or
      from the first line of standard input.
  beenfard admin token [-db URL] -username NAME -name LABEL [-expires DURATION]
      Issue an API token for a user and print it. Use it as
      "Authorization: Bearer <token>", e.g. for the Terraform provider.`

// runAdmin implements the offline "beenfard admin ..." subcommands, which
// work directly on the database.
func runAdmin(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("missing admin command\n%s", adminUsage)
	}
	cmd, args := args[0], args[1:]
	fs := flag.NewFlagSet("beenfard admin "+cmd, flag.ContinueOnError)
	dbURL := fs.String("db", envOr("BEENFAR_DB", "sqlite://beenfar.db"), "database URL (env BEENFAR_DB)")
	username := fs.String("username", envOr("BEENFAR_ADMIN_USER", "admin"), "username (env BEENFAR_ADMIN_USER)")

	ctx := context.Background()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	switch cmd {
	case "create":
		isAdmin := fs.Bool("admin", true, "grant the admin role")
		if err := fs.Parse(args); err != nil {
			return err
		}
		password := os.Getenv("BEENFAR_ADMIN_PASSWORD")
		if password == "" {
			line, err := bufio.NewReader(stdin).ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return fmt.Errorf("reading password: %w", err)
			}
			password = strings.TrimRight(line, "\r\n")
		}
		st, _, err := openStore(ctx, *dbURL)
		if err != nil {
			return err
		}
		defer func() { _ = st.Close() }()
		u, err := auth.New(st).CreateUser(ctx, *username, password, *isAdmin)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "created user %s (id %s, admin=%t)\n", u.Username, u.ID, u.Admin)
		return err

	case "token":
		name := fs.String("name", "", "token label, e.g. terraform")
		expires := fs.Duration("expires", 0, "token lifetime, e.g. 720h; 0 means it never expires")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *name == "" {
			return errors.New("-name is required")
		}
		st, _, err := openStore(ctx, *dbURL)
		if err != nil {
			return err
		}
		defer func() { _ = st.Close() }()
		u, err := st.GetUserByUsername(ctx, *username)
		if err != nil {
			return fmt.Errorf("user %q: %w", *username, err)
		}
		var expire time.Time
		if *expires > 0 {
			expire = time.Now().Add(*expires)
		}
		_, secret, err := auth.New(st).CreateAPIToken(ctx, u.ID, *name, expire)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, secret)
		return err

	default:
		return fmt.Errorf("unknown admin command %q\n%s", cmd, adminUsage)
	}
}
