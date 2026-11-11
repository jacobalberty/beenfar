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
