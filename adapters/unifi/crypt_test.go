package unifi

import "testing"

// The first vector is from Drepper's specification of SHA-512 crypt.
func TestSHA512Crypt(t *testing.T) {
	got, err := sha512Crypt("Hello world!", "saltstring")
	if err != nil {
		t.Fatal(err)
	}
	const want = "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	for _, salt := range []string{"", "seventeen-chars-x", "a$b"} {
		if _, err := sha512Crypt("x", salt); err == nil {
			t.Errorf("salt %q accepted", salt)
		}
	}
}
