// Package main 解析 BTC raw block/raw transaction。
// 生产扫块服务应优先从自建 Bitcoin Core getblock(hash, 0) 获取 raw block，再按本地地址表匹配 UTXO。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

const sampleBlockHash = "0000000000000000000162f179adec6f69571824971aa1fa5e78a6074be22864"

func main() {
	mode := flag.String("mode", "parse-block", "parse-block | parse-tx")
	blockHash := flag.String("block-hash", sampleBlockHash, "BTC block hash")
	rawBlockHex := flag.String("raw-block-hex", "", "Bitcoin Core getblock(hash, 0) raw block hex")
	rawBlockFile := flag.String("raw-block-file", "", "raw block binary file or hex text file")
	rawTxHex := flag.String("raw-tx-hex", "", "raw transaction hex")
	source := flag.String("source", "blockstream", "parse-block 数据源：blockstream | rpc")
	rpcURL := flag.String("rpc-url", "http://127.0.0.1:8332", "Bitcoin Core RPC URL")
	rpcUser := flag.String("rpc-user", "", "Bitcoin Core RPC user")
	rpcPass := flag.String("rpc-pass", "", "Bitcoin Core RPC password")
	flag.Parse()

	switch *mode {
	case "parse-block":
		if err := runParseBlock(*blockHash, *rawBlockHex, *rawBlockFile, *source, *rpcURL, *rpcUser, *rpcPass); err != nil {
			exitErr(err)
		}
	case "parse-tx":
		if *rawTxHex == "" {
			exitErr(fmt.Errorf("--raw-tx-hex is required"))
		}
		tx, err := ParseRawTx(*rawTxHex, mainnetHRP)
		if err != nil {
			exitErr(err)
		}
		printTx(*tx, 0)
	default:
		exitErr(fmt.Errorf("unknown mode %q", *mode))
	}
}

func runParseBlock(blockHash, rawBlockHex, rawBlockFile, source, rpcURL, rpcUser, rpcPass string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if rawBlockFile != "" {
		data, err := os.ReadFile(rawBlockFile)
		if err != nil {
			return err
		}
		text := strings.TrimSpace(string(data))
		if isHexString(text) {
			rawBlockHex = text
		} else {
			rawBlockHex = fmt.Sprintf("%x", data)
		}
	}
	if rawBlockHex == "" {
		switch source {
		case "blockstream":
			fmt.Printf("[拉取区块] source=blockstream hash=%s\n", blockHash)
			hexData, err := FetchBlockHexFromBlockstream(ctx, blockHash)
			if err != nil {
				return err
			}
			rawBlockHex = hexData
		case "rpc":
			fmt.Printf("[拉取区块] source=bitcoin-core-rpc hash=%s\n", blockHash)
			hexData, err := (bitcoinRPCClient{URL: rpcURL, User: rpcUser, Password: rpcPass}).GetBlockHex(ctx, blockHash)
			if err != nil {
				return err
			}
			rawBlockHex = hexData
		default:
			return fmt.Errorf("unknown source %q", source)
		}
	}
	block, err := ParseRawBlock(strings.TrimSpace(rawBlockHex), mainnetHRP)
	if err != nil {
		return err
	}
	printBlock(block)
	return nil
}

func printBlock(block *ParsedBlock) {
	fmt.Println("[区块解析]")
	fmt.Printf("  hash:       %s\n", block.Hash)
	fmt.Printf("  prev:       %s\n", block.PrevBlock)
	fmt.Printf("  merkle:     %s\n", block.MerkleRoot)
	fmt.Printf("  time:       %s\n", block.Timestamp.Format(time.RFC3339))
	fmt.Printf("  tx_count:   %d\n", len(block.Transactions))
	fmt.Printf("  output_sum: %d sat\n", block.TotalOutputSat)
	fmt.Println()
	limit := len(block.Transactions)
	if limit > 8 {
		limit = 8
	}
	for i := 0; i < limit; i++ {
		printTx(block.Transactions[i], i)
	}
	if len(block.Transactions) > limit {
		fmt.Printf("  ... 其余 %d 笔交易省略，生产扫块会逐笔解析并匹配本地地址表。\n", len(block.Transactions)-limit)
	}
}

func printTx(tx ParsedTx, index int) {
	fmt.Printf("[tx %d] txid=%s wtxid=%s inputs=%d outputs=%d vsize=%d coinbase=%v witness=%v\n",
		index, tx.TxID, tx.WTxID, len(tx.Inputs), len(tx.Outputs), tx.VSize, tx.IsCoinbase, tx.HasWitness)
	for i, in := range tx.Inputs {
		if i >= 3 {
			fmt.Printf("  vin ... 其余 %d 个输入省略\n", len(tx.Inputs)-i)
			break
		}
		if in.IsCoinbase {
			fmt.Printf("  vin[%d] coinbase script_len=%d sequence=%d\n", i, len(in.ScriptSigHex)/2, in.Sequence)
		} else {
			fmt.Printf("  vin[%d] prev=%s:%d witness_items=%d sequence=%d\n", i, in.PrevTxID, in.PrevVout, in.WitnessItems, in.Sequence)
		}
	}
	for i, out := range tx.Outputs {
		if i >= 5 {
			fmt.Printf("  vout ... 其余 %d 个输出省略\n", len(tx.Outputs)-i)
			break
		}
		addr := out.Address
		if addr == "" {
			addr = "-"
		}
		fmt.Printf("  vout[%d] %d sat type=%s address=%s script=%s\n", out.Index, out.AmountSat, out.Type, addr, out.ScriptPubKeyHex)
	}
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}
