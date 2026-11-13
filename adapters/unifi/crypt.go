package unifi

import (
	"bytes"
	"crypto/sha512"
	"errors"
	"strings"
)

// sha512Crypt computes the crypt(3) "$6$" hash of password with salt (at most
// 16 characters of the crypt alphabet), at the default 5000 rounds. A device's
// users.N.password is such a hash. It follows Drepper's "Unix crypto with
// SHA-256/SHA-512" and is checked against its published test vectors.
func sha512Crypt(password, salt string) (string, error) {
	if len(salt) == 0 || len(salt) > 16 || strings.ContainsAny(salt, "$:\n") {
		return "", errors.New("unifi: a crypt salt is 1-16 characters without '$' or ':'")
	}
	pw, sl := []byte(password), []byte(salt)

	b := sha512.New()
	b.Write(pw)
	b.Write(sl)
	b.Write(pw)
	sumB := b.Sum(nil)

	a := sha512.New()
	a.Write(pw)
	a.Write(sl)
	for n := len(pw); n > 0; n -= 64 {
		if n > 64 {
			a.Write(sumB)
		} else {
			a.Write(sumB[:n])
		}
	}
	for n := len(pw); n > 0; n >>= 1 {
		if n&1 != 0 {
			a.Write(sumB)
		} else {
			a.Write(pw)
		}
	}
	sumA := a.Sum(nil)

	dp := sha512.New()
	for range pw {
		dp.Write(pw)
	}
	p := repeatTo(dp.Sum(nil), len(pw))

	ds := sha512.New()
	for range 16 + int(sumA[0]) {
		ds.Write(sl)
	}
	s := repeatTo(ds.Sum(nil), len(sl))

	for i := range 5000 {
		c := sha512.New()
		if i&1 != 0 {
			c.Write(p)
		} else {
			c.Write(sumA)
		}
		if i%3 != 0 {
			c.Write(s)
		}
		if i%7 != 0 {
			c.Write(p)
		}
		if i&1 != 0 {
			c.Write(sumA)
		} else {
			c.Write(p)
		}
		sumA = c.Sum(nil)
	}

	const alphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var out bytes.Buffer
	enc := func(b2, b1, b0 byte, n int) {
		w := uint(b2)<<16 | uint(b1)<<8 | uint(b0)
		for range n {
			out.WriteByte(alphabet[w&0x3f])
			w >>= 6
		}
	}
	for _, g := range [][3]int{
		{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4}, {47, 5, 26}, {6, 27, 48}, {28, 49, 7},
		{50, 8, 29}, {9, 30, 51}, {31, 52, 10}, {53, 11, 32}, {12, 33, 54}, {34, 55, 13}, {56, 14, 35}, {15, 36, 57},
		{37, 58, 16}, {59, 17, 38}, {18, 39, 60}, {40, 61, 19}, {62, 20, 41},
	} {
		enc(sumA[g[0]], sumA[g[1]], sumA[g[2]], 4)
	}
	enc(0, 0, sumA[63], 2)
	return "$6$" + salt + "$" + out.String(), nil
}

func repeatTo(block []byte, n int) []byte {
	out := make([]byte, 0, n)
	for len(out) < n {
		out = append(out, block[:min(len(block), n-len(out))]...)
	}
	return out
}
