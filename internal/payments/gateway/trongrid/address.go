package trongrid

import (
	"bytes"
	"crypto/sha256"
	"math/big"
	"strings"
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// ValidAddress reports whether s is a TRON mainnet address in base58check:
// "T" and 33 more characters that decode to 0x41, 20 bytes and a checksum
// (the first 4 bytes of the double SHA-256 of the 21 before). A typo in the
// store's address would send customers' money where nobody can spend it, so
// the checksum is verified, not only the shape.
func ValidAddress(s string) bool {
	if len(s) != 34 || s[0] != 'T' {
		return false
	}
	n := new(big.Int)
	radix := big.NewInt(58)
	for i := 0; i < len(s); i++ {
		d := strings.IndexByte(base58Alphabet, s[i])
		if d < 0 {
			return false
		}
		n.Mul(n, radix)
		n.Add(n, big.NewInt(int64(d)))
	}
	b := n.Bytes()
	if len(b) != 25 || b[0] != 0x41 {
		return false
	}
	first := sha256.Sum256(b[:21])
	second := sha256.Sum256(first[:])
	return bytes.Equal(second[:4], b[21:])
}
