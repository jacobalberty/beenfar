package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jacobalberty/beenfar/adapters/unifi"
	"github.com/jacobalberty/beenfar/internal/unifisettings"
)

func TestAdminCreateAndToken(t *testing.T) {
	t.Setenv("BEENFAR_ADMIN_PASSWORD", "")
	db := "sqlite://" + filepath.Join(t.TempDir(), "beenfar.db")

	var out bytes.Buffer
	if err := runAdmin([]string{"create", "-db", db, "-username", "root"}, strings.NewReader("s3cret-password\n"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "created user root") {
		t.Fatalf("output %q", out.String())
	}

	out.Reset()
	if err := runAdmin([]string{"token", "-db", db, "-username", "root", "-name", "terraform"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if tok := strings.TrimSpace(out.String()); !strings.HasPrefix(tok, "bf_") {
		t.Fatalf("token %q", tok)
	}

	if err := runAdmin([]string{"token", "-db", db, "-username", "ghost", "-name", "x"}, nil, &out); err == nil {
		t.Error("token for unknown user succeeded")
	}
	if err := runAdmin([]string{"create", "-db", db, "-username", "weak"}, strings.NewReader("short\n"), &out); err == nil {
		t.Error("short password accepted")
	}
	if err := runAdmin([]string{"bogus"}, nil, &out); err == nil {
		t.Error("unknown subcommand accepted")
	}
}

func TestAdminAdapterToken(t *testing.T) {
	db := "sqlite://" + filepath.Join(t.TempDir(), "beenfar.db")

	var out bytes.Buffer
	if err := runAdmin([]string{"adapter-token", "-db", db, "create", "-adapter", "meraki"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	tok := strings.TrimSpace(out.String())
	if !strings.HasPrefix(tok, "bfa_") {
		t.Fatalf("token %q", tok)
	}

	out.Reset()
	if err := runAdmin([]string{"adapter-token", "-db", db, "list"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(out.String())
	fields := strings.Split(line, "\t")
	if len(fields) != 4 || fields[1] != "meraki" || strings.Contains(line, tok) {
		t.Fatalf("list output %q", out.String())
	}

	out.Reset()
	if err := runAdmin([]string{"adapter-token", "-db", db, "revoke", "-id", fields[0]}, nil, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runAdmin([]string{"adapter-token", "-db", db, "list"}, nil, &out); err != nil || out.Len() != 0 {
		t.Fatalf("list after revoke = %q, %v", out.String(), err)
	}

	for _, args := range [][]string{
		{"adapter-token", "-db", db},
		{"adapter-token", "-db", db, "create"},
		{"adapter-token", "-db", db, "create", "-adapter", "Bad Name"},
		{"adapter-token", "-db", db, "revoke"},
		{"adapter-token", "-db", db, "bogus"},
	} {
		if err := runAdmin(args, nil, &out); err == nil {
			t.Errorf("runAdmin(%v) succeeded", args)
		}
	}
}

func TestAdminImportUnifi(t *testing.T) {
	db := "sqlite://" + filepath.Join(t.TempDir(), "beenfar.db")
	key := "e2c930683af3945e4d0d58d37a78c2a6"

	var out bytes.Buffer
	in := `[{"mac": "F0:9F:C2:79:63:90", "authkey": "` + key + `", "gcm": false}, {"mac": "fc-ec-da-fc-c5-35", "authkey": "05223125e6b44aca0a0599551168d766", "gcm": true}]`
	if err := runAdmin([]string{"import-unifi", "-db", db}, strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "2 devices") || strings.Contains(out.String(), key) {
		t.Fatalf("output %q", out.String())
	}

	// Invalid input is refused whole, and an unknown field is a mistake.
	for name, bad := range map[string]string{
		"bad key":       `[{"mac": "de:ad:be:ef:00:01", "authkey": "abc"}]`,
		"unknown field": `[{"mac": "de:ad:be:ef:00:01", "authkey": "` + key + `", "authKey": "x"}]`,
		"not a list":    `{"mac": "de:ad:be:ef:00:01"}`,
	} {
		out.Reset()
		if err := runAdmin([]string{"import-unifi", "-db", db}, strings.NewReader(bad), &out); err == nil {
			t.Errorf("%s: import succeeded", name)
		}
	}
}

func TestAdminUnifiDevicePassword(t *testing.T) {
	ctx := t.Context()
	db := "sqlite://" + filepath.Join(t.TempDir(), "beenfar.db")

	// Before the daemon has generated the site settings there is nothing to show.
	if err := runAdmin([]string{"unifi-device-password", "-db", db}, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("printed a password that does not exist")
	}
	st, _, err := openStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := unifisettings.Ensure(ctx, st, unifi.SiteOptions{Country: 840}); err != nil {
		t.Fatal(err)
	}
	site, _ := unifisettings.Load(ctx, st)
	_ = st.Close()

	var out bytes.Buffer
	if err := runAdmin([]string{"unifi-device-password", "-db", db}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "user: admin") || !strings.Contains(out.String(), "password: "+site.AdminPassword) {
		t.Fatalf("output %q", out.String())
	}
}
