// Package main MPC 密钥管理演示：Shamir 秘密分享
// 展示 MPC 基础构件：私钥 → 分割份额 → 任意 k 个可恢复 → 验证
package main

import (
	"bytes"
	"fmt"
)

func main() {
	fmt.Println("=== 07 MPC Key Management Demo (Shamir Secret Sharing) ===")
	fmt.Println()

	// 模拟一个 32 字节私钥（生产中这是真实的 secp256k1 私钥）
	secret := []byte("exchange-wallet-private-key-demo") // 32 bytes

	fmt.Println("[Step 1] 私钥分割（2-of-3 Shamir 秘密分享）")
	fmt.Printf("  原始私钥: %x\n", secret)
	fmt.Println("  分割为 3 个份额，任意 2 个可恢复")
	fmt.Println()

	shares, err := Split(secret, 2, 3)
	if err != nil {
		fmt.Printf("[ERROR] Split: %v\n", err)
		return
	}

	for i, share := range shares {
		fmt.Printf("  份额 %d (给节点 %s):\n", i+1, []string{"A", "B", "C"}[i])
		fmt.Printf("    x = %s\n", share.X)
		y := share.Y.String()
		if len(y) > 30 {
			y = y[:30] + "..."
		}
		fmt.Printf("    y = %s\n", y)
	}
	fmt.Println()

	fmt.Println("[Step 2] 恢复原始私钥（使用份额1和份额3）")
	recovered, err := Reconstruct([]Share{shares[0], shares[2]})
	if err != nil {
		fmt.Printf("[ERROR] Reconstruct: %v\n", err)
		return
	}
	fmt.Printf("  恢复结果: %x\n", recovered)
	fmt.Printf("  与原始一致: %v\n", bytes.Equal(recovered, secret))
	fmt.Println()

	fmt.Println("[Step 3] 验证单个份额无法恢复")
	_, err = Reconstruct([]Share{shares[0]})
	if err != nil {
		fmt.Printf("  ✓ 单个份额无法恢复: %v\n", err)
	}
	fmt.Println()

	fmt.Println("[Step 4] 不同份额组合恢复结果相同")
	r1, _ := Reconstruct([]Share{shares[0], shares[1]})
	r2, _ := Reconstruct([]Share{shares[1], shares[2]})
	r3, _ := Reconstruct([]Share{shares[0], shares[2]})
	fmt.Printf("  份额1+2 恢复: 与原始一致=%v\n", bytes.Equal(r1, secret))
	fmt.Printf("  份额2+3 恢复: 与原始一致=%v\n", bytes.Equal(r2, secret))
	fmt.Printf("  份额1+3 恢复: 与原始一致=%v\n", bytes.Equal(r3, secret))
	fmt.Println()

	fmt.Println("=== Demo 完成 ===")
	fmt.Println("Shamir 秘密分享是 MPC 门限签名的基础构件：")
	fmt.Println("  完整私钥通过 DKG 从未出现（DKG = 分布式密钥生成）")
	fmt.Println("  每个 MPC 节点只持有一个份额")
	fmt.Println("  签名需要 k 个节点协作，无需合并私钥")
	fmt.Println()
	fmt.Println("完整 CGGMP21/FROST 实现见 MPC 学习项目：")
	fmt.Println("  https://github.com/yys9517/mpc-threshold-signature")
}
