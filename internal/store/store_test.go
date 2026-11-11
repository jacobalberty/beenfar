package store

import (
	"errors"
	"testing"
)

func TestNormalizeMAC(t *testing.T) {
	for in, want := range map[string]string{
		"deadbeef0000":      "de:ad:be:ef:00:00",
		"DE:AD:BE:EF:00:00": "de:ad:be:ef:00:00",
		"de-ad-be-ef-00-00": "de:ad:be:ef:00:00",
		"dead.beef.0000":    "de:ad:be:ef:00:00",
	} {
		got, err := NormalizeMAC(in)
		if err != nil || got != want {
			t.Errorf("NormalizeMAC(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "deadbeef", "deadbeef00001", "zz:ad:be:ef:00:00", "de ad be ef 00 00"} {
		if _, err := NormalizeMAC(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("NormalizeMAC(%q) error = %v, want ErrInvalid", in, err)
		}
	}
}
