package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type ParsedBlock struct {
	Hash           string
	Version        int32
	PrevBlock      string
	MerkleRoot     string
	Timestamp      time.Time
	Bits           uint32
	Nonce          uint32
	Transactions   []ParsedTx
	TotalInputSat  int64
	TotalOutputSat int64
}

type ParsedTx struct {
	TxID           string
	WTxID          string
	Version        int32
	LockTime       uint32
	Inputs         []ParsedInput
	Outputs        []ParsedOutput
	IsCoinbase     bool
	HasWitness     bool
	VSize          int
	TotalOutputSat int64
}

type ParsedInput struct {
	PrevTxID     string
	PrevVout     uint32
	Sequence     uint32
	ScriptSigHex string
	WitnessItems int
	IsCoinbase   bool
}

type ParsedOutput struct {
	Index           uint32
	AmountSat       int64
	ScriptPubKeyHex string
	Address         string
	Type            string
	Purpose         string
}

// ParseRawBlock 解析 Bitcoin Core getblock 0 返回的 raw block hex。
// 生产扫块服务通常从 getblock(hash, 0) 或 P2P raw block 开始，先做二进制解析，再按本地地址表筛选 UTXO。
func ParseRawBlock(rawHex string, hrp string) (*ParsedBlock, error) {
	raw, err := hex.DecodeString(rawHex)
	if err != nil {
		return nil, err
	}
	r := bytes.NewReader(raw)
	header := make([]byte, 80)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("read block header: %w", err)
	}
	block := &ParsedBlock{
		Hash:       displayHash(doubleSHA256(header)),
		Version:    int32(binary.LittleEndian.Uint32(header[0:4])),
		PrevBlock:  displayHash(bytesToHash(header[4:36])),
		MerkleRoot: displayHash(bytesToHash(header[36:68])),
		Timestamp:  time.Unix(int64(binary.LittleEndian.Uint32(header[68:72])), 0).UTC(),
		Bits:       binary.LittleEndian.Uint32(header[72:76]),
		Nonce:      binary.LittleEndian.Uint32(header[76:80]),
	}
	txCount, err := readVarInt(r)
	if err != nil {
		return nil, fmt.Errorf("read tx count: %w", err)
	}
	for i := uint64(0); i < txCount; i++ {
		tx, err := readTx(r, hrp)
		if err != nil {
			return nil, fmt.Errorf("parse tx[%d]: %w", i, err)
		}
		block.Transactions = append(block.Transactions, tx)
		block.TotalOutputSat += tx.TotalOutputSat
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("raw block has %d trailing bytes", r.Len())
	}
	return block, nil
}

func ParseRawTx(rawHex string, hrp string) (*ParsedTx, error) {
	raw, err := hex.DecodeString(rawHex)
	if err != nil {
		return nil, err
	}
	r := bytes.NewReader(raw)
	tx, err := readTx(r, hrp)
	if err != nil {
		return nil, err
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("raw tx has %d trailing bytes", r.Len())
	}
	return &tx, nil
}

func readTx(r *bytes.Reader, hrp string) (ParsedTx, error) {
	startLen := r.Len()
	versionBytes := make([]byte, 4)
	if _, err := io.ReadFull(r, versionBytes); err != nil {
		return ParsedTx{}, err
	}
	tx := ParsedTx{Version: int32(binary.LittleEndian.Uint32(versionBytes))}

	var markerFlag []byte
	first, err := r.ReadByte()
	if err != nil {
		return ParsedTx{}, err
	}
	if first == 0x00 {
		flag, err := r.ReadByte()
		if err != nil {
			return ParsedTx{}, err
		}
		if flag == 0x00 {
			return ParsedTx{}, errors.New("invalid witness marker flag")
		}
		tx.HasWitness = true
		markerFlag = []byte{0x00, flag}
	} else {
		if err := r.UnreadByte(); err != nil {
			return ParsedTx{}, err
		}
	}

	inputCount, err := readVarInt(r)
	if err != nil {
		return ParsedTx{}, err
	}
	inputRaw := bytes.NewBuffer(nil)
	inputRaw.Write(encodeVarInt(inputCount))
	for i := uint64(0); i < inputCount; i++ {
		in, raw, err := readInput(r)
		if err != nil {
			return ParsedTx{}, err
		}
		tx.Inputs = append(tx.Inputs, in)
		inputRaw.Write(raw)
	}

	outputCount, err := readVarInt(r)
	if err != nil {
		return ParsedTx{}, err
	}
	outputRaw := bytes.NewBuffer(nil)
	outputRaw.Write(encodeVarInt(outputCount))
	for i := uint64(0); i < outputCount; i++ {
		out, raw, err := readOutput(r, uint32(i), hrp)
		if err != nil {
			return ParsedTx{}, err
		}
		tx.Outputs = append(tx.Outputs, out)
		tx.TotalOutputSat += out.AmountSat
		outputRaw.Write(raw)
	}

	witnessRaw := bytes.NewBuffer(nil)
	if tx.HasWitness {
		for i := range tx.Inputs {
			itemCount, err := readVarInt(r)
			if err != nil {
				return ParsedTx{}, err
			}
			witnessRaw.Write(encodeVarInt(itemCount))
			tx.Inputs[i].WitnessItems = int(itemCount)
			for j := uint64(0); j < itemCount; j++ {
				itemLen, err := readVarInt(r)
				if err != nil {
					return ParsedTx{}, err
				}
				item := make([]byte, itemLen)
				if _, err := io.ReadFull(r, item); err != nil {
					return ParsedTx{}, err
				}
				witnessRaw.Write(encodeVarInt(itemLen))
				witnessRaw.Write(item)
			}
		}
	}
	lockBytes := make([]byte, 4)
	if _, err := io.ReadFull(r, lockBytes); err != nil {
		return ParsedTx{}, err
	}
	tx.LockTime = binary.LittleEndian.Uint32(lockBytes)

	// txid 不包含 witness；wtxid 包含 marker/flag 和 witness。交易所提现落库通常同时保存 txid/wtxid，外部查询主要用 txid。
	noWitness := bytes.NewBuffer(nil)
	noWitness.Write(versionBytes)
	noWitness.Write(inputRaw.Bytes())
	noWitness.Write(outputRaw.Bytes())
	noWitness.Write(lockBytes)
	withWitness := bytes.NewBuffer(nil)
	withWitness.Write(versionBytes)
	if tx.HasWitness {
		withWitness.Write(markerFlag)
	}
	withWitness.Write(inputRaw.Bytes())
	withWitness.Write(outputRaw.Bytes())
	withWitness.Write(witnessRaw.Bytes())
	withWitness.Write(lockBytes)

	tx.TxID = displayHash(doubleSHA256(noWitness.Bytes()))
	tx.WTxID = displayHash(doubleSHA256(withWitness.Bytes()))
	tx.IsCoinbase = len(tx.Inputs) == 1 && tx.Inputs[0].IsCoinbase
	baseSize := len(noWitness.Bytes())
	totalSize := startLen - r.Len()
	tx.VSize = virtualSize(baseSize, totalSize)
	return tx, nil
}

func readInput(r *bytes.Reader) (ParsedInput, []byte, error) {
	raw := bytes.NewBuffer(nil)
	prevHash := make([]byte, 32)
	if _, err := io.ReadFull(r, prevHash); err != nil {
		return ParsedInput{}, nil, err
	}
	raw.Write(prevHash)
	voutBytes := make([]byte, 4)
	if _, err := io.ReadFull(r, voutBytes); err != nil {
		return ParsedInput{}, nil, err
	}
	raw.Write(voutBytes)
	scriptLen, err := readVarInt(r)
	if err != nil {
		return ParsedInput{}, nil, err
	}
	raw.Write(encodeVarInt(scriptLen))
	script := make([]byte, scriptLen)
	if _, err := io.ReadFull(r, script); err != nil {
		return ParsedInput{}, nil, err
	}
	raw.Write(script)
	seqBytes := make([]byte, 4)
	if _, err := io.ReadFull(r, seqBytes); err != nil {
		return ParsedInput{}, nil, err
	}
	raw.Write(seqBytes)

	vout := binary.LittleEndian.Uint32(voutBytes)
	isCoinbase := bytes.Equal(prevHash, make([]byte, 32)) && vout == 0xffffffff
	return ParsedInput{
		PrevTxID:     displayHash(bytesToHash(prevHash)),
		PrevVout:     vout,
		Sequence:     binary.LittleEndian.Uint32(seqBytes),
		ScriptSigHex: hex.EncodeToString(script),
		IsCoinbase:   isCoinbase,
	}, raw.Bytes(), nil
}

func readOutput(r *bytes.Reader, index uint32, hrp string) (ParsedOutput, []byte, error) {
	raw := bytes.NewBuffer(nil)
	valueBytes := make([]byte, 8)
	if _, err := io.ReadFull(r, valueBytes); err != nil {
		return ParsedOutput{}, nil, err
	}
	raw.Write(valueBytes)
	scriptLen, err := readVarInt(r)
	if err != nil {
		return ParsedOutput{}, nil, err
	}
	raw.Write(encodeVarInt(scriptLen))
	script := make([]byte, scriptLen)
	if _, err := io.ReadFull(r, script); err != nil {
		return ParsedOutput{}, nil, err
	}
	raw.Write(script)
	out := ParsedOutput{
		Index:           index,
		AmountSat:       int64(binary.LittleEndian.Uint64(valueBytes)),
		ScriptPubKeyHex: hex.EncodeToString(script),
		Type:            scriptType(script),
		Address:         scriptAddress(script, hrp),
	}
	return out, raw.Bytes(), nil
}

func scriptType(script []byte) string {
	switch {
	case len(script) == 22 && script[0] == 0x00 && script[1] == 0x14:
		return "p2wpkh"
	case len(script) == 34 && script[0] == 0x00 && script[1] == 0x20:
		return "p2wsh"
	case len(script) == 25 && script[0] == 0x76 && script[1] == 0xa9 && script[2] == 0x14 && script[23] == 0x88 && script[24] == 0xac:
		return "p2pkh"
	case len(script) == 23 && script[0] == 0xa9 && script[1] == 0x14 && script[22] == 0x87:
		return "p2sh"
	case len(script) >= 2 && script[0] == 0x6a:
		return "op_return"
	case len(script) == 34 && script[0] == 0x51 && script[1] == 0x20:
		return "p2tr"
	default:
		return "unknown"
	}
}

func readVarInt(r *bytes.Reader) (uint64, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	switch prefix {
	case 0xfd:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		return uint64(binary.LittleEndian.Uint16(b[:])), nil
	case 0xfe:
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		return uint64(binary.LittleEndian.Uint32(b[:])), nil
	case 0xff:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint64(b[:]), nil
	default:
		return uint64(prefix), nil
	}
}

func bytesToHash(b []byte) [32]byte {
	var h [32]byte
	copy(h[:], b)
	return h
}

func isHexString(s string) bool {
	if len(s) == 0 || len(s)%2 != 0 {
		return false
	}
	for _, r := range strings.ToLower(s) {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
