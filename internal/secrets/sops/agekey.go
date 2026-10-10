package sops

import (
	"crypto/ecdh"
	"errors"
	"strings"
)

// age keys are Bech32 strings (BIP 173): an X25519 secret key is
// AGE-SECRET-KEY-1… and its public recipient age1…. Deriving one from the
// other takes the standard library's X25519 and the few lines of Bech32
// below, which saves den the age module and everything it pulls in.

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

var bech32Gen = [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}

func bech32Polymod(values []byte) uint32 {
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := range 5 {
			if (top>>i)&1 == 1 {
				chk ^= bech32Gen[i]
			}
		}
	}
	return chk
}

func bech32HRPExpand(hrp string) []byte {
	out := make([]byte, 0, len(hrp)*2+1)
	for i := range len(hrp) {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := range len(hrp) {
		out = append(out, hrp[i]&31)
	}
	return out
}

// convertBits regroups data from fromBits-wide to toBits-wide values.
func convertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	var acc, bits uint
	maxv := uint(1)<<toBits - 1
	var out []byte
	for _, v := range data {
		if uint(v)>>fromBits != 0 {
			return nil, errors.New("invalid data range")
		}
		acc = acc<<fromBits | uint(v)
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			out = append(out, byte(acc>>bits&maxv)) //nolint:gosec // G115: masked to toBits (≤ 8) bits
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, byte(acc<<(toBits-bits)&maxv)) //nolint:gosec // G115: masked to toBits (≤ 8) bits
		}
	} else if bits >= fromBits || acc<<(toBits-bits)&maxv != 0 {
		return nil, errors.New("invalid padding")
	}
	return out, nil
}

// bech32Decode returns the lowercased human-readable part and the data of s.
// Unlike BIP 173 it allows strings over 90 characters, as age does.
func bech32Decode(s string) (string, []byte, error) {
	if strings.ToLower(s) != s && strings.ToUpper(s) != s {
		return "", nil, errors.New("mixed case")
	}
	s = strings.ToLower(s)
	pos := strings.LastIndexByte(s, '1')
	if pos < 1 || pos+7 > len(s) {
		return "", nil, errors.New("separator misplaced")
	}
	hrp := s[:pos]
	values := make([]byte, 0, len(s)-pos-1)
	for i := pos + 1; i < len(s); i++ {
		d := strings.IndexByte(bech32Charset, s[i])
		if d < 0 {
			return "", nil, errors.New("invalid character")
		}
		values = append(values, byte(d)) //nolint:gosec // G115: d indexes the 32-character charset
	}
	if bech32Polymod(append(bech32HRPExpand(hrp), values...)) != 1 {
		return "", nil, errors.New("invalid checksum")
	}
	data, err := convertBits(values[:len(values)-6], 5, 8, false)
	return hrp, data, err
}

func bech32Encode(hrp string, data []byte) (string, error) {
	values, err := convertBits(data, 8, 5, true)
	if err != nil {
		return "", err
	}
	hrp = strings.ToLower(hrp)
	poly := bech32Polymod(append(append(bech32HRPExpand(hrp), values...), 0, 0, 0, 0, 0, 0)) ^ 1
	var b strings.Builder
	b.WriteString(hrp)
	b.WriteByte('1')
	for _, v := range values {
		b.WriteByte(bech32Charset[v])
	}
	for i := range 6 {
		b.WriteByte(bech32Charset[(poly>>(5*(5-i)))&31])
	}
	return b.String(), nil
}

// ageRecipient derives the public recipient (age1…) of an X25519 age secret
// key, or "" for anything else: post-quantum and plugin identities are left
// for sops to judge.
func ageRecipient(secret string) string {
	hrp, key, err := bech32Decode(secret)
	if err != nil || hrp != "age-secret-key-" || len(key) != 32 {
		return ""
	}
	priv, err := ecdh.X25519().NewPrivateKey(key)
	if err != nil {
		return ""
	}
	r, err := bech32Encode("age", priv.PublicKey().Bytes())
	if err != nil {
		return ""
	}
	return r
}
