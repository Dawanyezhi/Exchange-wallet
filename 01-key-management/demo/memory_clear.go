package main

import (
	"crypto/rand"
	"fmt"
	"runtime"
)

// Clear 四步内存清除，防止密钥数据残留在物理内存中（Cold Boot Attack 防护）。
func Clear(b []byte) {
	for i := range b {
		b[i] = 0x00
	}
	for i := range b {
		b[i] = 0xFF
	}
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = byte(i ^ 0x5A)
		}
	}
	for i := range b {
		b[i] = 0x00
	}
	runtime.KeepAlive(b)
}

// Mask XOR 掩码，使私钥在内存中不以明文形式存放。
type Mask struct {
	data [][]byte // [0]=masked value, [1]=mask
}

// NewMask 创建新的 XOR 掩码保护，传入的 data 会被立即清零。
func NewMask(data []byte) (*Mask, error) {
	mask := make([]byte, len(data))
	if _, err := rand.Read(mask); err != nil {
		return nil, fmt.Errorf("mask: generate random mask: %w", err)
	}

	masked := make([]byte, len(data))
	for i := range data {
		masked[i] = data[i] ^ mask[i]
	}
	Clear(data)

	return &Mask{
		data: [][]byte{masked, mask},
	}, nil
}

// Reveal 临时恢复明文数据，fn 执行后立即重新掩码。
func (m *Mask) Reveal(fn func(plaintext []byte)) {
	if m.data == nil {
		return
	}
	masked, mask := m.data[0], m.data[1]
	plain := make([]byte, len(masked))
	for i := range masked {
		plain[i] = masked[i] ^ mask[i]
	}
	fn(plain)
	Clear(plain)
}

// ClearMask 清除掩码和被保护的数据。
func (m *Mask) ClearMask() {
	if m.data == nil {
		return
	}
	for _, b := range m.data {
		Clear(b)
	}
	m.data = nil
}
