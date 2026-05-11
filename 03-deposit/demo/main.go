// Package main 充值 7 层防护演示程序。
// 运行：go run ./03-deposit/demo/
package main

import (
	"context"
	"fmt"
	"math/big"
)

func main() {
	fmt.Println("=== 03 Deposit 7-Layer Filter Demo ===")
	fmt.Println()

	config := &Config{
		Chain: "ETH",
		ManagedAddrs: map[string]int64{
			"0x1111111111111111111111111111111111111111": 1001,
			"0x2222222222222222222222222222222222222222": 1002,
		},
		HotAddrs: map[string]struct{}{
			"0xhotwallet000000000000000000000000000001": {},
		},
		Whitelist: map[string]TokenConfig{
			"0xdac17f958d2ee523a2206206994597c13d831ec7": {
				Symbol: "USDT", Decimals: 6,
				Address: "0xdac17f958d2ee523a2206206994597c13d831ec7",
			},
		},
		VerifyFailPolicy: "ignore",
	}

	pipeline := NewPipeline(config, nil) // nil RPC 跳过第7层二次校验
	ctx := context.Background()

	testCases := []struct {
		name  string
		tx    *RawTransaction
		want  bool
		layer string // 被哪一层拦截
	}{
		// ── 正常充值 ──────────────────────────────────────────────────
		{
			name:  "✓ 有效 USDT 充值",
			layer: "全部通过",
			tx: &RawTransaction{
				Hash: "0xTx001", From: "0xExternal", To: "0x1111111111111111111111111111111111111111",
				Value: big.NewInt(0), BlockHash: "0xBlock100",
				Receipt: &Receipt{
					TxHash: "0xTx001", BlockHash: "0xBlock100", Status: 1,
					Logs: []*Log{{
						Address: "0xdac17f958d2ee523a2206206994597c13d831ec7",
						Topics:  []string{transferEventSig, "0x000...external", "0x0000000000000000000000001111111111111111111111111111111111111111"},
						Data:    "00000000000000000000000000000000000000000000000000000000000f4240",
						Removed: false,
					}},
				},
			},
			want: true,
		},

		// ── 第1层：receipt.status 攻击 ────────────────────────────────
		{
			name:  "✗ 失败交易（status=0，合约 revert）",
			layer: "Layer1",
			tx: &RawTransaction{
				Hash: "0xTx002", From: "0xExternal", To: "0x1111111111111111111111111111111111111111",
				Value: big.NewInt(0), BlockHash: "0xBlock100",
				Receipt: &Receipt{TxHash: "0xTx002", BlockHash: "0xBlock100", Status: 0},
			},
			want: false,
		},

		// ── 第2层：Transfer 事件日志攻击 ─────────────────────────────
		{
			name:  "✗ Removed log（重组期间被撤销的日志）",
			layer: "Layer2",
			tx: &RawTransaction{
				Hash: "0xTx003", From: "0xExternal", To: "0x1111111111111111111111111111111111111111",
				Value: big.NewInt(0), BlockHash: "0xBlock100",
				Receipt: &Receipt{
					TxHash: "0xTx003", BlockHash: "0xBlock100", Status: 1,
					Logs: []*Log{{
						Address: "0xdac17f958d2ee523a2206206994597c13d831ec7",
						Topics:  []string{transferEventSig, "0x000...ext", "0x0000000000000000000000001111111111111111111111111111111111111111"},
						Data:    "00000000000000000000000000000000000000000000000000000000000f4240",
						Removed: true, // 重组时设为 true，不能上账
					}},
				},
			},
			want: false,
		},
		{
			name:  "✗ 金额为 0 的 Transfer 事件",
			layer: "Layer2",
			tx: &RawTransaction{
				Hash: "0xTx004", From: "0xExternal", To: "0x1111111111111111111111111111111111111111",
				Value: big.NewInt(0), BlockHash: "0xBlock100",
				Receipt: &Receipt{
					TxHash: "0xTx004", BlockHash: "0xBlock100", Status: 1,
					Logs: []*Log{{
						Address: "0xdac17f958d2ee523a2206206994597c13d831ec7",
						Topics:  []string{transferEventSig, "0x000...ext", "0x0000000000000000000000001111111111111111111111111111111111111111"},
						Data:    "0000000000000000000000000000000000000000000000000000000000000000", // amount=0
						Removed: false,
					}},
				},
			},
			want: false,
		},

		// ── 第3层：Token 白名单攻击 ───────────────────────────────────
		{
			name:  "✗ 假 USDT 合约（地址不在白名单）",
			layer: "Layer3",
			tx: &RawTransaction{
				Hash: "0xTx005", From: "0xExternal", To: "0x1111111111111111111111111111111111111111",
				Value: big.NewInt(0), BlockHash: "0xBlock100",
				Receipt: &Receipt{
					TxHash: "0xTx005", BlockHash: "0xBlock100", Status: 1,
					Logs: []*Log{{
						Address: "0xFAKEUSdT0000000000000000000000000000001", // 假合约地址
						Topics:  []string{transferEventSig, "0x000...ext", "0x0000000000000000000000001111111111111111111111111111111111111111"},
						Data:    "00000000000000000000000000000000000000000000000000000000000f4240",
						Removed: false,
					}},
				},
			},
			want: false,
		},

		// ── 第4层：BlockHash 一致性攻击 ──────────────────────────────
		{
			name:  "✗ BlockHash 不一致（重组期间交易被打包进不同区块）",
			layer: "Layer4",
			tx: &RawTransaction{
				Hash: "0xTx006", From: "0xExternal", To: "0x1111111111111111111111111111111111111111",
				Value: big.NewInt(0),
				BlockHash: "0xBlock200", // 当前处理的区块
				Receipt: &Receipt{
					TxHash: "0xTx006",
					BlockHash: "0xBlock100", // receipt 记录的是不同区块 → 重组异常
					Status: 1,
					Logs: []*Log{{
						Address: "0xdac17f958d2ee523a2206206994597c13d831ec7",
						Topics:  []string{transferEventSig, "0x000...ext", "0x0000000000000000000000001111111111111111111111111111111111111111"},
						Data:    "00000000000000000000000000000000000000000000000000000000000f4240",
						Removed: false,
					}},
				},
			},
			want: false,
		},

		// ── 第5层：内部调用 Trace 攻击 ───────────────────────────────
		{
			name:  "✗ 主调用（depth=0）失败 → 合约整体回滚",
			layer: "Layer5",
			tx: &RawTransaction{
				Hash: "0xTx007", From: "0xExternal", To: "0x1111111111111111111111111111111111111111",
				Value: big.NewInt(0), BlockHash: "0xBlock100",
				Receipt: &Receipt{
					TxHash: "0xTx007", BlockHash: "0xBlock100", Status: 1,
					Logs: []*Log{{
						Address: "0xdac17f958d2ee523a2206206994597c13d831ec7",
						Topics:  []string{transferEventSig, "0x000...ext", "0x0000000000000000000000001111111111111111111111111111111111111111"},
						Data:    "00000000000000000000000000000000000000000000000000000000000f4240",
						Removed: false,
					}},
				},
				Traces: []*Trace{
					{CallType: "call", From: "0xExternal", To: "0xContract", Value: "0xf4240", Depth: 0, Error: "execution reverted"},
				},
			},
			want: false,
		},

		// ── 第6层：交易方向分类攻击 ───────────────────────────────────
		{
			name:  "✗ 热钱包发出（提现被误判为充值）",
			layer: "Layer6",
			tx: &RawTransaction{
				Hash: "0xTx008", From: "0xhotwallet000000000000000000000000000001",
				To: "0x1111111111111111111111111111111111111111",
				Value: big.NewInt(1000000000000000000), BlockHash: "0xBlock100",
				Receipt: &Receipt{
					TxHash: "0xTx008", BlockHash: "0xBlock100", Status: 1,
					Logs: []*Log{{
						Address: "0xdac17f958d2ee523a2206206994597c13d831ec7",
						Topics:  []string{transferEventSig, "0x000...hot", "0x0000000000000000000000001111111111111111111111111111111111111111"},
						Data:    "00000000000000000000000000000000000000000000000000000000000f4240",
						Removed: false,
					}},
				},
			},
			want: false,
		},
		{
			name:  "✗ 收款方不是受管地址（转给陌生地址）",
			layer: "Layer6",
			tx: &RawTransaction{
				Hash: "0xTx009", From: "0xExternal",
				To: "0x9999999999999999999999999999999999999999", // 未注册地址
				Value: big.NewInt(0), BlockHash: "0xBlock100",
				Receipt: &Receipt{
					TxHash: "0xTx009", BlockHash: "0xBlock100", Status: 1,
					Logs: []*Log{{
						Address: "0xdac17f958d2ee523a2206206994597c13d831ec7",
						Topics:  []string{transferEventSig, "0x000...ext", "0x0000000000000000000000009999999999999999999999999999999999999999"},
						Data:    "00000000000000000000000000000000000000000000000000000000000f4240",
						Removed: false,
					}},
				},
			},
			want: false,
		},
	}

	passed := 0
	for _, tc := range testCases {
		deposit, err := pipeline.Process(ctx, tc.tx)
		if err != nil {
			fmt.Printf("  [ERROR] %s: %v\n", tc.name, err)
			continue
		}
		got := deposit != nil
		if got != tc.want {
			fmt.Printf("  FAIL  [%s] %s (got=%v want=%v)\n", tc.layer, tc.name, got, tc.want)
		} else {
			passed++
			fmt.Printf("  PASS  [%s] %s\n", tc.layer, tc.name)
		}
	}

	fmt.Printf("\n结果：%d/%d 通过\n", passed, len(testCases))
	fmt.Println()
	fmt.Println("=== 7层纵深防御设计原则 ===")
	fmt.Println("  Layer1  receipt.status=1    → 拦截合约 revert 失败交易")
	fmt.Println("  Layer2  Transfer 事件解析   → 拦截 Removed log / 零金额转账")
	fmt.Println("  Layer3  合约地址白名单       → 拦截假 USDT 合约攻击")
	fmt.Println("  Layer4  BlockHash 一致性     → 拦截重组期间的双花攻击")
	fmt.Println("  Layer5  内部交易 Trace       → 拦截合约子调用回滚")
	fmt.Println("  Layer6  交易方向分类          → 拦截提现误判为充值")
	fmt.Println("  Layer7  二次 RPC 校验        → 防止节点层面数据污染")
	fmt.Println()
	fmt.Println("  纵深防御：每层独立，攻击者必须同时绕过全部 7 层才能成功")
}
