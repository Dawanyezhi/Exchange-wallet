package main

import (
	"crypto/rand"
	"fmt"
	"runtime"
)

// Clear 四步内存清除，防止密钥数据残留在物理内存中。
// 四步设计原因：编译器会优化掉"无意义的"单次覆盖写，多步骤 + 随机值让优化器无法消除这些写操作。
// 防护目标：Cold Boot Attack（物理内存冻结读取）、/proc/mem 扫描、core dump。
func Clear(b []byte) {
	// Step 1: 全写 0x00（标准清零）
	for i := range b {
		b[i] = 0x00
	}
	// Step 2: 全写 0xFF（与 Step 1 形成对比，防编译器把两次写优化成一次）
	for i := range b {
		b[i] = 0xFF
	}
	// Step 3: 写入真随机数（非确定性，编译器无法预测，绝对无法优化掉）
	if _, err := rand.Read(b); err != nil {
		// rand.Read 在 Linux/macOS 几乎不会失败，降级为伪随机覆盖
		for i := range b {
			b[i] = byte(i ^ 0x5A)
		}
	}
	// Step 4: 再次全写 0x00，最终状态为全零，符合"清除"语义
	for i := range b {
		b[i] = 0x00
	}
	// KeepAlive 告诉 GC 和编译器：b 在这里仍然被引用，禁止提前回收或优化掉上面的写操作
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
