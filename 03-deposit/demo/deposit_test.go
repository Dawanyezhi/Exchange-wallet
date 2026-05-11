package main

import (
	"context"
	"math/big"
	"testing"
)

const (
	knownUSDT     = "0xdac17f958d2ee523a2206206994597c13d831ec7"
	fakeUSDT      = "0xFakeUSDT000000000000000000000000000000001"
	userAddress   = "0x1111111111111111111111111111111111111111"
	externalAddr  = "0x9999999999999999999999999999999999999999"
	hotWallet     = "0xHotWallet0000000000000000000000000000000"

	validTransferLog_To    = "0x0000000000000000000000001111111111111111111111111111111111111111"
	validTransferLog_From  = "0x0000000000000000000000009999999999999999999999999999999999999999"
	// 1 USDT = 1000000 (6 decimals), hex padded to 32 bytes
	validTransferLog_Amount = "00000000000000000000000000000000000000000000000000000000000f4240"
)

func makeValidTx() *RawTransaction {
	return &RawTransaction{
		Hash:        "0xValidTx",
		From:        externalAddr,
		To:          userAddress,
		Value:       big.NewInt(0),
		BlockHash:   "0xBlock1",
		BlockHeight: 100,
		Receipt: &Receipt{
			TxHash:    "0xValidTx",
			BlockHash: "0xBlock1",
			Status:    1,
			Logs: []*Log{
				{
					Address: knownUSDT,
					Topics: []string{
						transferEventSig,
						validTransferLog_From,
						validTransferLog_To,
					},
					Data:    validTransferLog_Amount,
					Removed: false,
				},
			},
		},
	}
}

func makeWhitelist() map[string]TokenConfig {
	return map[string]TokenConfig{
		knownUSDT: {Symbol: "USDT", Decimals: 6, Address: knownUSDT},
	}
}

func makeManagedAddrs() map[string]int64 {
	return map[string]int64{userAddress: 1001}
}

func makeHotAddrs() map[string]struct{} {
	return map[string]struct{}{hotWallet: {}}
}

// TestLayer1_FailedTransaction 第1层：status=0x0 的交易被过滤。
func TestLayer1_FailedTransaction(t *testing.T) {
	filter := NewLayer1ReceiptFilter()
	ctx := context.Background()

	tx := makeValidTx()
	tx.Receipt.Status = 0 // 失败交易

	pass, err := filter.Filter(ctx, tx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass {
		t.Error("failed transaction (status=0) should be filtered")
	}
}

func TestLayer1_SuccessTransaction(t *testing.T) {
	filter := NewLayer1ReceiptFilter()
	pass, _ := filter.Filter(context.Background(), makeValidTx())
	if !pass {
		t.Error("success transaction should pass layer1")
	}
}

// TestLayer2_InvalidTopics 第2层：Topics 格式错误被过滤。
func TestLayer2_InvalidTopics(t *testing.T) {
	filter := NewLayer2EventLogFilter()
	ctx := context.Background()

	tx := makeValidTx()
	// 只有2个 Topics（缺少 to 地址）
	tx.Receipt.Logs[0].Topics = []string{transferEventSig, validTransferLog_From}

	pass, _ := filter.Filter(ctx, tx)
	if pass {
		t.Error("log with 2 topics should be filtered")
	}
}

// TestLayer2_RemovedLog 第2层：log.Removed=true 被过滤。
func TestLayer2_RemovedLog(t *testing.T) {
	filter := NewLayer2EventLogFilter()
	ctx := context.Background()

	tx := makeValidTx()
	tx.Receipt.Logs[0].Removed = true // 重组期间被撤销

	pass, _ := filter.Filter(ctx, tx)
	if pass {
		t.Error("removed log should be filtered")
	}
}

// TestLayer3_FakeToken 第3层：假 USDT 合约（symbol 相同但地址不在白名单）被过滤。
func TestLayer3_FakeToken(t *testing.T) {
	filter := NewLayer3WhitelistFilter(makeWhitelist())
	ctx := context.Background()

	tx := makeValidTx()
	tx.Receipt.Logs[0].Address = fakeUSDT // 假合约地址

	pass, _ := filter.Filter(ctx, tx)
	if pass {
		t.Error("fake USDT (not in whitelist) should be filtered")
	}
}

func TestLayer3_ValidToken(t *testing.T) {
	filter := NewLayer3WhitelistFilter(makeWhitelist())
	pass, _ := filter.Filter(context.Background(), makeValidTx())
	if !pass {
		t.Error("valid USDT should pass layer3")
	}
}

// TestLayer4_BlockHashMismatch 第4层：BlockHash 不一致被过滤。
func TestLayer4_BlockHashMismatch(t *testing.T) {
	filter := NewLayer4BlockHashFilter()
	ctx := context.Background()

	tx := makeValidTx()
	tx.BlockHash = "0xBlock1"
	tx.Receipt.BlockHash = "0xBlock999" // 不一致，重组时交易被打包到其他块

	pass, _ := filter.Filter(ctx, tx)
	if pass {
		t.Error("blockHash mismatch should be filtered")
	}
}

// TestLayer5_TraceMainCallFailed 第5层：主调用失败时整个交易作废。
func TestLayer5_TraceMainCallFailed(t *testing.T) {
	traces := []*Trace{
		{CallType: "call", Depth: 0, Error: "execution reverted",
			From: externalAddr, To: userAddress, Value: "0x1"},
		{CallType: "call", Depth: 1, Error: "",
			From: externalAddr, To: userAddress, Value: "0xDE0B6B3A7640000"},
	}
	managedAddrs := map[string]int64{userAddress: 1001}
	deposits := FilterWithTraces(traces, managedAddrs)
	if len(deposits) != 0 {
		t.Errorf("main call failed, expected 0 deposits, got %d", len(deposits))
	}
}

// TestLayer5_SubCallFailed 第5层：子调用失败只跳过该子树，不影响其他调用。
func TestLayer5_SubCallFailed(t *testing.T) {
	traces := []*Trace{
		{CallType: "call", Depth: 0, Error: "",
			From: externalAddr, To: "0xContract", Value: "0x0"},
		{CallType: "call", Depth: 1, Error: "execution reverted", // 子调用失败
			From: "0xContract", To: userAddress, Value: "0xDE0B6B3A7640000"},
		{CallType: "call", Depth: 1, Error: "", // 另一个子调用成功
			From: "0xContract", To: userAddress, Value: "0xDE0B6B3A7640000"},
	}
	managedAddrs := map[string]int64{userAddress: 1001}
	deposits := FilterWithTraces(traces, managedAddrs)
	if len(deposits) != 1 {
		t.Errorf("expected 1 deposit (from successful sub-call), got %d", len(deposits))
	}
}

// TestLayer6_HotWalletFiltered 第6层：热钱包发出的转账（提现）被过滤。
func TestLayer6_HotWalletFiltered(t *testing.T) {
	filter := NewLayer6ClassifierFilter(makeManagedAddrs(), makeHotAddrs())
	ctx := context.Background()

	tx := makeValidTx()
	tx.From = hotWallet // 热钱包 → 用户地址 = 提现，不是充值

	pass, _ := filter.Filter(ctx, tx)
	if pass {
		t.Error("hot wallet → user should be filtered (it's a withdrawal, not deposit)")
	}
}

// TestLayer7_VerifyIgnoreDeposit 第7层：节点返回不一致数据时，验证 ignore 行为。
func TestLayer7_VerifyIgnoreDeposit(t *testing.T) {
	// 模拟第二个节点返回不同的 blockHash
	mockRPC := &mockRPCForVerify{
		receipt: &Receipt{
			TxHash:    "0xValidTx",
			BlockHash: "0xDifferentBlock", // 不同的 blockHash
			Status:    1,
		},
	}
	filter := NewLayer7VerifyFilter(mockRPC, "ignore")
	ctx := context.Background()

	pass, err := filter.Filter(ctx, makeValidTx())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass {
		t.Error("second verify failure with 'ignore' policy should filter the deposit")
	}
}

type mockRPCForVerify struct {
	receipt *Receipt
	err     error
}

func (m *mockRPCForVerify) GetTransactionReceipt(_ context.Context, _ string) (*Receipt, error) {
	return m.receipt, m.err
}
