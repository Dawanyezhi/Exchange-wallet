package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	mode := flag.String("mode", "sample", "sample | parse-block | parse-transaction")
	inputFile := flag.String("input-file", "", "getBlock/getTransaction result JSON file")
	rpcURL := flag.String("rpc-url", "https://api.mainnet-beta.solana.com", "Solana JSON-RPC URL")
	slot := flag.Uint64("slot", 0, "slot for getBlock")
	signature := flag.String("signature", "", "signature for getTransaction")
	flag.Parse()

	var err error
	switch *mode {
	case "sample":
		err = runSample()
	case "parse-block":
		err = runParseBlock(*inputFile, *rpcURL, *slot)
	case "parse-transaction":
		err = runParseTransaction(*inputFile, *rpcURL, *signature)
	default:
		err = fmt.Errorf("unknown mode %q", *mode)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func runSample() error {
	return printBlockJSON([]byte(sampleBlockJSON))
}

func runParseBlock(inputFile string, rpcURL string, slot uint64) error {
	raw, err := loadOrFetch(inputFile, func(ctx context.Context) ([]byte, error) {
		if slot == 0 {
			return nil, fmt.Errorf("--slot is required when --input-file is empty")
		}
		return (solanaRPCClient{URL: rpcURL}).GetBlock(ctx, slot)
	})
	if err != nil {
		return err
	}
	return printBlockJSON(raw)
}

func runParseTransaction(inputFile string, rpcURL string, signature string) error {
	raw, err := loadOrFetch(inputFile, func(ctx context.Context) ([]byte, error) {
		if signature == "" {
			return nil, fmt.Errorf("--signature is required when --input-file is empty")
		}
		return (solanaRPCClient{URL: rpcURL}).GetTransaction(ctx, signature)
	})
	if err != nil {
		return err
	}
	var tx RPCTransaction
	if err := jsonUnmarshal(raw, &tx); err != nil {
		return err
	}
	summary, err := ParseTransaction(tx)
	if err != nil {
		return err
	}
	printTx(summary, 0)
	return nil
}

func loadOrFetch(inputFile string, fetch func(context.Context) ([]byte, error)) ([]byte, error) {
	if inputFile != "" {
		return os.ReadFile(inputFile)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return fetch(ctx)
}

func printBlockJSON(raw []byte) error {
	block, err := ParseBlockJSON(raw)
	if err != nil {
		return err
	}
	fmt.Println("[区块解析]")
	fmt.Printf("  blockhash: %s\n", block.Blockhash)
	fmt.Printf("  previous:  %s\n", block.PreviousBlockhash)
	fmt.Printf("  height:    %d\n", block.BlockHeight)
	fmt.Printf("  blockTime: %d\n", block.BlockTime)
	fmt.Printf("  tx_count:  %d\n", block.TxCount)
	fmt.Println()
	limit := len(block.Transactions)
	if limit > 8 {
		limit = 8
	}
	for i := 0; i < limit; i++ {
		printTx(block.Transactions[i], i)
	}
	if len(block.Transactions) > limit {
		fmt.Printf("  ... 其余 %d 笔交易省略，生产扫块会逐笔解析并匹配本地地址/Token Account 表。\n", len(block.Transactions)-limit)
	}
	return nil
}

func printTx(tx ParsedTxSummary, index int) {
	fmt.Printf("[tx %d] signature=%s fee_payer=%s fee=%d err=%v\n", index, tx.Signature, tx.FeePayer, tx.FeeLamports, tx.Err)
	for _, transfer := range tx.SOLTransfers {
		fmt.Printf("  SOL transfer %s -> %s  %d lamports source=%s\n", transfer.From, transfer.To, transfer.Lamports, transfer.Source)
	}
	for _, delta := range tx.BalanceDeltas {
		fmt.Printf("  balance %s pre=%d post=%d delta=%d\n", delta.Address, delta.PreLamports, delta.PostLamports, delta.DeltaLamports)
	}
	for _, delta := range tx.TokenDeltas {
		fmt.Printf("  token account=%s owner=%s mint=%s delta=%s decimals=%d program=%s\n",
			delta.Account, delta.Owner, delta.Mint, delta.DeltaBaseUnit, delta.Decimals, delta.ProgramID)
	}
}

func jsonUnmarshal(raw []byte, out interface{}) error {
	return json.Unmarshal(raw, out)
}
