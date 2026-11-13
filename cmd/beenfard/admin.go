package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jacobalberty/beenfar/adapters/unifi"
	"github.com/jacobalberty/beenfar/internal/adapterhost"
	"github.com/jacobalberty/beenfar/internal/auth"
	"github.com/jacobalberty/beenfar/internal/unifisettings"
)

const adminUsage = `usage:
  beenfard admin create [-db URL] [-username NAME] [-admin=true]
      Create a user. The password is read from BEENFAR_ADMIN_PASSWORD, or
      from the first line of standard input.
  beenfard admin token [-db URL] -username NAME -name LABEL [-expires DURATION]
      Issue an API token for a user and print it. Use it as
      "Authorization: Bearer <token>", e.g. for the Terraform provider.
  beenfard admin adapter-token [-db URL] create -adapter NAME
  beenfard admin adapter-token [-db URL] list
  beenfard admin adapter-token [-db URL] revoke -id ID
      Manage the tokens external adapters use to call the adapter API.
      "create" prints the token once; the token lets its holder act as the
      adapter NAME.
  beenfard admin import-unifi [-db URL] [-file PATH]
      Import the credentials of UniFi devices managed by another controller,
      from JSON [{"mac": "...", "authkey": "32 hex digits", "gcm": false}] in
      PATH or on standard input. Once such a device is pointed at beenfar
      (DNS or set-inform) it appears as pending with its real model, and
      adopting it re-keys it. The keys are never shown by any API.
  beenfard admin unifi-device-password [-db URL]
      Print the password of the "admin" account that beenfar sets on UniFi
      access points it provisions (SSH, console). It is generated at first
      start and shown by nothing else: treat the output as a secret.`

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

	case "import-unifi":
		file := fs.String("file", "", "JSON file to read (default: standard input)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		r := stdin
		if *file != "" {
			f, err := os.Open(*file)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			r = f
		}
		var in []unifi.KeyImport
		dec := json.NewDecoder(r)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			return fmt.Errorf("reading device keys: %w", err)
		}
		st, _, err := openStore(ctx, *dbURL)
		if err != nil {
			return err
		}
		defer func() { _ = st.Close() }()
		n, err := unifi.ImportKeys(ctx, adapterhost.NewHost(unifi.Name, st), in)
		if err != nil {
			return fmt.Errorf("imported %d of %d devices: %w", n, len(in), err)
		}
		_, err = fmt.Fprintf(stdout, "imported keys for %d devices\n", n)
		return err

	case "unifi-device-password":
		if err := fs.Parse(args); err != nil {
			return err
		}
		st, _, err := openStore(ctx, *dbURL)
		if err != nil {
			return err
		}
		defer func() { _ = st.Close() }()
		site, err := unifisettings.Load(ctx, st)
		if err != nil {
			return fmt.Errorf("no UniFi site settings yet (start beenfard once): %w", err)
		}
		_, err = fmt.Fprintf(stdout, "user: %s\npassword: %s\n", site.AdminUser, site.AdminPassword)
		return err

	case "adapter-token":
		return runAdapterToken(ctx, fs, dbURL, args, stdout)

	default:
		return fmt.Errorf("unknown admin command %q\n%s", cmd, adminUsage)
	}
}

// runAdapterToken implements "admin adapter-token create|list|revoke".
//
// The subcommand follows the shared flags ("-db URL create -adapter NAME"),
// so the flags are parsed once to find it and again for its own flags.
func runAdapterToken(ctx context.Context, fs *flag.FlagSet, dbURL *string, args []string, stdout io.Writer) error {
	adapterName := fs.String("adapter", "", "adapter name, e.g. meraki")
	id := fs.String("id", "", "token id, from \"adapter-token list\"")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("missing adapter-token command\n%s", adminUsage)
	}
	sub := fs.Arg(0)
	if err := fs.Parse(fs.Args()[1:]); err != nil {
		return err
	}
	st, _, err := openStore(ctx, *dbURL)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	svc := auth.New(st)

	switch sub {
	case "create":
		if *adapterName == "" {
			return errors.New("-adapter is required")
		}
		_, secret, err := svc.CreateAdapterToken(ctx, *adapterName)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, secret)
		return err
	case "list":
		tokens, err := svc.ListAdapterTokens(ctx)
		if err != nil {
			return err
		}
		for _, t := range tokens {
			last := "never"
			if !t.LastUsedTime.IsZero() {
				last = t.LastUsedTime.Format(time.RFC3339)
			}
			if _, err := fmt.Fprintf(stdout, "%s\t%s\tbfa_%s_...\tlast used %s\n", t.ID, t.Adapter, t.Prefix, last); err != nil {
				return err
			}
		}
		return nil
	case "revoke":
		if *id == "" {
			return errors.New("-id is required")
		}
		if err := svc.DeleteAdapterToken(ctx, *id); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "revoked %s\n", *id)
		return err
	default:
		return fmt.Errorf("unknown adapter-token command %q\n%s", sub, adminUsage)
	}
}
