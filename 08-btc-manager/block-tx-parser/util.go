package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
)

const (
	mainnetHRP = "bc"
)

func doubleSHA256(data []byte) [32]byte {
	first := sha256.Sum256(data)
	return sha256.Sum256(first[:])
}

func displayHash(in [32]byte) string {
	out := make([]byte, 32)
	for i := range in {
		out[i] = in[31-i]
	}
	return hex.EncodeToString(out)
}

func encodeVarInt(v uint64) []byte {
	switch {
	case v < 0xfd:
		return []byte{byte(v)}
	case v <= 0xffff:
		var b [3]byte
		b[0] = 0xfd
		binary.LittleEndian.PutUint16(b[1:], uint16(v))
		return b[:]
	case v <= 0xffffffff:
		var b [5]byte
		b[0] = 0xfe
		binary.LittleEndian.PutUint32(b[1:], uint32(v))
		return b[:]
	default:
		var b [9]byte
		b[0] = 0xff
		binary.LittleEndian.PutUint64(b[1:], v)
		return b[:]
	}
}

func virtualSize(baseSize, totalSize int) int {
	weight := baseSize*3 + totalSize
	return int(math.Ceil(float64(weight) / 4))
}
