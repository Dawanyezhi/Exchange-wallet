package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ripemd160"
)

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

var bech32CharsetRev = func() map[rune]int {
	out := make(map[rune]int, len(bech32Charset))
	for i, r := range bech32Charset {
		out[r] = i
	}
	return out
}()

// hash160 是 BTC P2PKH/P2WPKH 地址常用的 HASH160(pubkey)。
func hash160(data []byte) []byte {
	sha := sha256.Sum256(data)
	h := ripemd160.New()
	_, _ = h.Write(sha[:])
	return h.Sum(nil)
}

// EncodeP2WPKHAddress 把 20 字节 pubKeyHash 编码为 bech32 P2WPKH 地址。
func EncodeP2WPKHAddress(hrp string, pubKeyHash []byte) (string, error) {
	if len(pubKeyHash) != 20 {
		return "", fmt.Errorf("p2wpkh pubkey hash must be 20 bytes, got %d", len(pubKeyHash))
	}
	data, err := convertBits(pubKeyHash, 8, 5, true)
	if err != nil {
		return "", err
	}
	return bech32Encode(strings.ToLower(hrp), append([]byte{0}, data...))
}

// DecodeSegwitAddress 解析 bech32/bech32m 隔离见证地址，demo 只接受 witness v0 P2WPKH。
func DecodeSegwitAddress(address string) (hrp string, version byte, program []byte, err error) {
	hrp, data, err := bech32Decode(address)
	if err != nil {
		return "", 0, nil, err
	}
	if len(data) == 0 {
		return "", 0, nil, errors.New("segwit address: missing witness version")
	}
	version = data[0]
	if version > 16 {
		return "", 0, nil, fmt.Errorf("segwit address: invalid witness version %d", version)
	}
	program, err = convertBits(data[1:], 5, 8, false)
	if err != nil {
		return "", 0, nil, err
	}
	if version == 0 && len(program) != 20 && len(program) != 32 {
		return "", 0, nil, fmt.Errorf("segwit v0 program must be 20 or 32 bytes, got %d", len(program))
	}
	return hrp, version, program, nil
}

func p2wpkhScript(pubKeyHash []byte) ([]byte, error) {
	if len(pubKeyHash) != 20 {
		return nil, fmt.Errorf("p2wpkh script needs 20-byte hash, got %d", len(pubKeyHash))
	}
	script := make([]byte, 0, 22)
	script = append(script, 0x00, 0x14)
	script = append(script, pubKeyHash...)
	return script, nil
}

func p2wpkhScriptCode(pubKeyHash []byte) ([]byte, error) {
	if len(pubKeyHash) != 20 {
		return nil, fmt.Errorf("p2wpkh scriptCode needs 20-byte hash, got %d", len(pubKeyHash))
	}
	// 这里返回的是 scriptCode 脚本体；BIP143 preimage 序列化时会另外写入 compactSize 长度。
	script := make([]byte, 0, 24)
	script = append(script, 0x76, 0xa9, 0x14)
	script = append(script, pubKeyHash...)
	script = append(script, 0x88, 0xac)
	return script, nil
}

func scriptAddress(script []byte, hrp string) string {
	if len(script) == 22 && script[0] == 0x00 && script[1] == 0x14 {
		addr, err := EncodeP2WPKHAddress(hrp, script[2:])
		if err == nil {
			return addr
		}
	}
	return ""
}

func bech32Encode(hrp string, data []byte) (string, error) {
	if len(hrp) == 0 {
		return "", errors.New("bech32: empty hrp")
	}
	combined := append(data, bech32CreateChecksum(hrp, data)...)
	var b strings.Builder
	b.Grow(len(hrp) + 1 + len(combined))
	b.WriteString(hrp)
	b.WriteByte('1')
	for _, p := range combined {
		if p >= 32 {
			return "", fmt.Errorf("bech32: data value out of range: %d", p)
		}
		b.WriteByte(bech32Charset[p])
	}
	return b.String(), nil
}

func bech32Decode(bech string) (string, []byte, error) {
	if bech != strings.ToLower(bech) && bech != strings.ToUpper(bech) {
		return "", nil, errors.New("bech32: mixed case address")
	}
	bech = strings.ToLower(bech)
	pos := strings.LastIndexByte(bech, '1')
	if pos < 1 || pos+7 > len(bech) {
		return "", nil, fmt.Errorf("bech32: invalid separator position in %q", bech)
	}
	hrp := bech[:pos]
	dataPart := bech[pos+1:]
	data := make([]byte, len(dataPart))
	for i, r := range dataPart {
		v, ok := bech32CharsetRev[r]
		if !ok {
			return "", nil, fmt.Errorf("bech32: invalid charset %q", r)
		}
		data[i] = byte(v)
	}
	if !bech32VerifyChecksum(hrp, data) {
		return "", nil, errors.New("bech32: checksum mismatch")
	}
	return hrp, data[:len(data)-6], nil
}

func bech32HrpExpand(hrp string) []byte {
	out := make([]byte, 0, len(hrp)*2+1)
	for _, r := range hrp {
		out = append(out, byte(r>>5))
	}
	out = append(out, 0)
	for _, r := range hrp {
		out = append(out, byte(r&31))
	}
	return out
}

func bech32CreateChecksum(hrp string, data []byte) []byte {
	values := append(bech32HrpExpand(hrp), data...)
	values = append(values, 0, 0, 0, 0, 0, 0)
	polymod := bech32Polymod(values) ^ 1
	out := make([]byte, 6)
	for i := range out {
		out[i] = byte((polymod >> uint(5*(5-i))) & 31)
	}
	return out
}

func bech32VerifyChecksum(hrp string, data []byte) bool {
	return bech32Polymod(append(bech32HrpExpand(hrp), data...)) == 1
}

func bech32Polymod(values []byte) uint32 {
	chk := uint32(1)
	generator := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (top>>uint(i))&1 == 1 {
				chk ^= generator[i]
			}
		}
	}
	return chk
}

func convertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	acc := uint(0)
	bits := uint(0)
	maxv := uint((1 << toBits) - 1)
	maxAcc := uint((1 << (fromBits + toBits - 1)) - 1)
	ret := make([]byte, 0, len(data)*int(fromBits)/int(toBits))
	for _, value := range data {
		v := uint(value)
		if v>>fromBits != 0 {
			return nil, fmt.Errorf("convert bits: value %d exceeds %d bits", value, fromBits)
		}
		acc = ((acc << fromBits) | v) & maxAcc
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			ret = append(ret, byte((acc>>bits)&maxv))
		}
	}
	if pad {
		if bits > 0 {
			ret = append(ret, byte((acc<<(toBits-bits))&maxv))
		}
	} else if bits >= fromBits || ((acc<<(toBits-bits))&maxv) != 0 {
		return nil, errors.New("convert bits: invalid padding")
	}
	return ret, nil
}
