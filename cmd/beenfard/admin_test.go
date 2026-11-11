package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
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
