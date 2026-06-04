package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	systemProgramID = "11111111111111111111111111111111"
	lamportsPerSOL  = int64(1_000_000_000)
)

type TransferRequest struct {
	RequestID               string
	BusinessID              int64
	Network                 string
	FromAddress             string
	ToAddress               string
	AmountLamports          int64
	FeePayerAddress         string
	RecentBlockhash         string
	LastValidBlockHeight    uint64
	MaxBaseFeeLamports      int64
	ExpectedSignerPath      string
	ExpectedSignerPubkey    []byte
	SignerPrivateKey        *slip10PrivateKey
	AllowOnlySystemTransfer bool
}

type SignRequest struct {
	RequestID            string
	MessageBytes         []byte
	MessageSummary       MessageSummary
	ExpectedSignerPath   string
	ExpectedSignerPubkey []byte
	SignerPrivateKey     *slip10PrivateKey
	Policy               SignPolicy
}

type SignPolicy struct {
	MaxBaseFeeLamports      int64
	RequireNoExtraProgram   bool
	AllowedProgramIDs       map[string]bool
	ExpectedFromAddress     string
	ExpectedToAddress       string
	ExpectedAmountLamports  int64
	ExpectedFeePayerAddress string
}

type MessageSummary struct {
	FeePayer             string
	RecentBlockhash      string
	LastValidBlockHeight uint64
	Transfer             TransferInstructionSummary
	AccountKeys          []AccountMeta
}

type TransferInstructionSummary struct {
	ProgramID string
	From      string
	To        string
	Lamports  int64
}

type AccountMeta struct {
	Pubkey   string
	Signer   bool
	Writable bool
	Role     string
}

type SignResponse struct {
	RequestID    string
	Approved     bool
	Signature    []byte
	SignerPubkey []byte
	AuditID      string
	RejectReason string
}

type SignedTransaction struct {
	RawTxBase64 string
	Signature   string
	MessageHash string
	FeeLamports int64
	Summary     ParsedTransaction
}

type LegacyMessage struct {
	Header          MessageHeader
	AccountKeys     [][32]byte
	RecentBlockhash [32]byte
	Instructions    []CompiledInstruction
}

type MessageHeader struct {
	NumRequiredSignatures       byte
	NumReadonlySignedAccounts   byte
	NumReadonlyUnsignedAccounts byte
}

type CompiledInstruction struct {
	ProgramIDIndex byte
	Accounts       []byte
	Data           []byte
}

type ParsedTransaction struct {
	Signature       string
	FeePayer        string
	RecentBlockhash string
	From            string
	To              string
	Lamports        int64
	ProgramID       string
	AccountKeys     []string
}

func BuildSOLTransferSignRequest(req TransferRequest) (*SignRequest, error) {
	if req.Network == "" {
		req.Network = "mainnet-beta"
	}
	if req.AmountLamports <= 0 {
		return nil, errors.New("amount_lamports must be positive")
	}
	if req.MaxBaseFeeLamports == 0 {
		req.MaxBaseFeeLamports = 10_000
	}
	if req.FeePayerAddress == "" {
		req.FeePayerAddress = req.FromAddress
	}
	from, err := mustDecodePubkey(req.FromAddress)
	if err != nil {
		return nil, fmt.Errorf("from address: %w", err)
	}
	to, err := mustDecodePubkey(req.ToAddress)
	if err != nil {
		return nil, fmt.Errorf("to address: %w", err)
	}
	feePayer, err := mustDecodePubkey(req.FeePayerAddress)
	if err != nil {
		return nil, fmt.Errorf("fee payer address: %w", err)
	}
	system, _ := mustDecodePubkey(systemProgramID)
	blockhash, err := mustDecodePubkey(req.RecentBlockhash)
	if err != nil {
		return nil, fmt.Errorf("recent blockhash: %w", err)
	}
	if !bytes.Equal(feePayer[:], from[:]) {
		return nil, errors.New("demo only supports fee payer == source hot wallet")
	}
	accountKeys := [][32]byte{feePayer, to, system}
	ix := CompiledInstruction{
		ProgramIDIndex: 2,
		Accounts:       []byte{0, 1},
		Data:           systemTransferData(req.AmountLamports),
	}
	message := LegacyMessage{
		Header: MessageHeader{
			NumRequiredSignatures:       1,
			NumReadonlySignedAccounts:   0,
			NumReadonlyUnsignedAccounts: 1,
		},
		AccountKeys:     accountKeys,
		RecentBlockhash: blockhash,
		Instructions:    []CompiledInstruction{ix},
	}
	messageBytes := message.serialize()
	summary := MessageSummary{
		FeePayer:             req.FeePayerAddress,
		RecentBlockhash:      req.RecentBlockhash,
		LastValidBlockHeight: req.LastValidBlockHeight,
		AccountKeys: []AccountMeta{
			{Pubkey: req.FeePayerAddress, Signer: true, Writable: true, Role: "fee_payer_and_source"},
			{Pubkey: req.ToAddress, Signer: false, Writable: true, Role: "withdraw_target"},
			{Pubkey: systemProgramID, Signer: false, Writable: false, Role: "system_program"},
		},
		Transfer: TransferInstructionSummary{
			ProgramID: systemProgramID,
			From:      req.FromAddress,
			To:        req.ToAddress,
			Lamports:  req.AmountLamports,
		},
	}
	signReq := &SignRequest{
		RequestID:            req.RequestID,
		MessageBytes:         messageBytes,
		MessageSummary:       summary,
		ExpectedSignerPath:   req.ExpectedSignerPath,
		ExpectedSignerPubkey: append([]byte(nil), req.ExpectedSignerPubkey...),
		SignerPrivateKey:     req.SignerPrivateKey,
		Policy: SignPolicy{
			MaxBaseFeeLamports:      req.MaxBaseFeeLamports,
			RequireNoExtraProgram:   true,
			AllowedProgramIDs:       map[string]bool{systemProgramID: true},
			ExpectedFromAddress:     req.FromAddress,
			ExpectedToAddress:       req.ToAddress,
			ExpectedAmountLamports:  req.AmountLamports,
			ExpectedFeePayerAddress: req.FeePayerAddress,
		},
	}
	if err := ValidateUnsignedMessage(signReq); err != nil {
		return nil, err
	}
	return signReq, nil
}

func ValidateUnsignedMessage(req *SignRequest) error {
	if req == nil || len(req.MessageBytes) == 0 {
		return errors.New("empty sign request")
	}
	parsed, err := ParseLegacyMessage(req.MessageBytes)
	if err != nil {
		return err
	}
	if len(parsed.AccountKeys) != 3 || len(parsed.Instructions) != 1 {
		return errors.New("SOL withdraw demo requires exactly 3 account keys and 1 instruction")
	}
	summary, err := summarizeTransferMessage(parsed)
	if err != nil {
		return err
	}
	// 签名前必须以反序列化 message 为准校验，不信任在线钱包服务给出的可读摘要。
	if summary.FeePayer != req.Policy.ExpectedFeePayerAddress {
		return fmt.Errorf("fee payer mismatch: %s", summary.FeePayer)
	}
	if summary.From != req.Policy.ExpectedFromAddress || summary.To != req.Policy.ExpectedToAddress {
		return fmt.Errorf("transfer account mismatch: from=%s to=%s", summary.From, summary.To)
	}
	if summary.Lamports != req.Policy.ExpectedAmountLamports {
		return fmt.Errorf("transfer lamports mismatch: %d", summary.Lamports)
	}
	if !req.Policy.AllowedProgramIDs[summary.ProgramID] {
		return fmt.Errorf("program id not allowed: %s", summary.ProgramID)
	}
	if req.Policy.MaxBaseFeeLamports > 0 && 5000 > req.Policy.MaxBaseFeeLamports {
		return fmt.Errorf("base fee exceeds policy: %d", req.Policy.MaxBaseFeeLamports)
	}
	return nil
}

func OfflineSignMessage(req *SignRequest) (*SignResponse, error) {
	if err := ValidateUnsignedMessage(req); err != nil {
		return &SignResponse{RequestID: req.RequestID, Approved: false, RejectReason: err.Error()}, err
	}
	pub := req.SignerPrivateKey.publicKey()
	if !bytes.Equal(pub, req.ExpectedSignerPubkey) {
		return nil, errors.New("signer pubkey mismatch")
	}
	// Solana ed25519 签名对象是完整 message bytes，不是 hash；签名机必须自己反解析 message 再签。
	sig := ed25519.Sign(req.SignerPrivateKey.privateKey(), req.MessageBytes)
	return &SignResponse{
		RequestID:    req.RequestID,
		Approved:     true,
		Signature:    append([]byte(nil), sig...),
		SignerPubkey: pub,
		AuditID:      "sol-hot-hsm-demo-000001",
	}, nil
}

func AssembleAndVerify(req *SignRequest, resp *SignResponse) (*SignedTransaction, error) {
	if resp == nil || !resp.Approved {
		return nil, fmt.Errorf("sign rejected: %s", resp.RejectReason)
	}
	if !ed25519.Verify(ed25519.PublicKey(resp.SignerPubkey), req.MessageBytes, resp.Signature) {
		return nil, errors.New("signature verification failed")
	}
	raw := serializeTransaction(resp.Signature, req.MessageBytes)
	parsed, err := ParseSignedTransaction(raw)
	if err != nil {
		return nil, err
	}
	// 签后反解析再次确认目标、金额、program id，避免签名后 rawtx 被替换。
	if parsed.FeePayer != req.Policy.ExpectedFeePayerAddress ||
		parsed.From != req.Policy.ExpectedFromAddress ||
		parsed.To != req.Policy.ExpectedToAddress ||
		parsed.Lamports != req.Policy.ExpectedAmountLamports ||
		parsed.ProgramID != systemProgramID {
		return nil, fmt.Errorf("signed transaction summary mismatch: %+v", parsed)
	}
	messageHash := sha256.Sum256(req.MessageBytes)
	return &SignedTransaction{
		RawTxBase64: base64.StdEncoding.EncodeToString(raw),
		Signature:   encodeBase58(resp.Signature),
		MessageHash: hex.EncodeToString(messageHash[:]),
		FeeLamports: 5000,
		Summary:     parsed,
	}, nil
}

func ParseLegacyMessage(raw []byte) (*LegacyMessage, error) {
	r := bytes.NewReader(raw)
	header := make([]byte, 3)
	if _, err := r.Read(header); err != nil {
		return nil, err
	}
	keyCount, err := readCompactU16(r)
	if err != nil {
		return nil, err
	}
	keys := make([][32]byte, keyCount)
	for i := range keys {
		if _, err := r.Read(keys[i][:]); err != nil {
			return nil, err
		}
	}
	var blockhash [32]byte
	if _, err := r.Read(blockhash[:]); err != nil {
		return nil, err
	}
	ixCount, err := readCompactU16(r)
	if err != nil {
		return nil, err
	}
	instructions := make([]CompiledInstruction, 0, ixCount)
	for i := uint64(0); i < ixCount; i++ {
		programIndex, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		accountCount, err := readCompactU16(r)
		if err != nil {
			return nil, err
		}
		accounts := make([]byte, accountCount)
		if _, err := r.Read(accounts); err != nil {
			return nil, err
		}
		dataLen, err := readCompactU16(r)
		if err != nil {
			return nil, err
		}
		data := make([]byte, dataLen)
		if _, err := r.Read(data); err != nil {
			return nil, err
		}
		instructions = append(instructions, CompiledInstruction{ProgramIDIndex: programIndex, Accounts: accounts, Data: data})
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("message has %d trailing bytes", r.Len())
	}
	return &LegacyMessage{
		Header: MessageHeader{
			NumRequiredSignatures:       header[0],
			NumReadonlySignedAccounts:   header[1],
			NumReadonlyUnsignedAccounts: header[2],
		},
		AccountKeys:     keys,
		RecentBlockhash: blockhash,
		Instructions:    instructions,
	}, nil
}

func ParseSignedTransaction(raw []byte) (ParsedTransaction, error) {
	r := bytes.NewReader(raw)
	sigCount, err := readCompactU16(r)
	if err != nil {
		return ParsedTransaction{}, err
	}
	if sigCount != 1 {
		return ParsedTransaction{}, fmt.Errorf("demo expects 1 signature, got %d", sigCount)
	}
	sig := make([]byte, 64)
	if _, err := r.Read(sig); err != nil {
		return ParsedTransaction{}, err
	}
	messageBytes := make([]byte, r.Len())
	if _, err := r.Read(messageBytes); err != nil {
		return ParsedTransaction{}, err
	}
	msg, err := ParseLegacyMessage(messageBytes)
	if err != nil {
		return ParsedTransaction{}, err
	}
	summary, err := summarizeTransferMessage(msg)
	if err != nil {
		return ParsedTransaction{}, err
	}
	if !ed25519.Verify(ed25519.PublicKey(msg.AccountKeys[0][:]), messageBytes, sig) {
		return ParsedTransaction{}, errors.New("raw transaction signature verification failed")
	}
	summary.Signature = encodeBase58(sig)
	summary.RecentBlockhash = encodeBase58(msg.RecentBlockhash[:])
	return summary, nil
}

func summarizeTransferMessage(msg *LegacyMessage) (ParsedTransaction, error) {
	if len(msg.AccountKeys) < 3 || len(msg.Instructions) != 1 {
		return ParsedTransaction{}, errors.New("unsupported message shape")
	}
	ix := msg.Instructions[0]
	if int(ix.ProgramIDIndex) >= len(msg.AccountKeys) || len(ix.Accounts) != 2 {
		return ParsedTransaction{}, errors.New("invalid transfer instruction indexes")
	}
	programID := encodeBase58(msg.AccountKeys[ix.ProgramIDIndex][:])
	fromIndex := ix.Accounts[0]
	toIndex := ix.Accounts[1]
	if int(fromIndex) >= len(msg.AccountKeys) || int(toIndex) >= len(msg.AccountKeys) {
		return ParsedTransaction{}, errors.New("transfer account index out of range")
	}
	lamports, err := parseSystemTransferData(ix.Data)
	if err != nil {
		return ParsedTransaction{}, err
	}
	keys := make([]string, len(msg.AccountKeys))
	for i := range msg.AccountKeys {
		keys[i] = encodeBase58(msg.AccountKeys[i][:])
	}
	return ParsedTransaction{
		FeePayer:        keys[0],
		RecentBlockhash: encodeBase58(msg.RecentBlockhash[:]),
		From:            keys[fromIndex],
		To:              keys[toIndex],
		Lamports:        lamports,
		ProgramID:       programID,
		AccountKeys:     keys,
	}, nil
}

func (m LegacyMessage) serialize() []byte {
	var out bytes.Buffer
	out.WriteByte(m.Header.NumRequiredSignatures)
	out.WriteByte(m.Header.NumReadonlySignedAccounts)
	out.WriteByte(m.Header.NumReadonlyUnsignedAccounts)
	out.Write(encodeCompactU16(uint64(len(m.AccountKeys))))
	for _, key := range m.AccountKeys {
		out.Write(key[:])
	}
	out.Write(m.RecentBlockhash[:])
	out.Write(encodeCompactU16(uint64(len(m.Instructions))))
	for _, ix := range m.Instructions {
		out.WriteByte(ix.ProgramIDIndex)
		out.Write(encodeCompactU16(uint64(len(ix.Accounts))))
		out.Write(ix.Accounts)
		out.Write(encodeCompactU16(uint64(len(ix.Data))))
		out.Write(ix.Data)
	}
	return out.Bytes()
}

func serializeTransaction(signature []byte, message []byte) []byte {
	var out bytes.Buffer
	out.Write(encodeCompactU16(1))
	out.Write(signature)
	out.Write(message)
	return out.Bytes()
}

func systemTransferData(lamports int64) []byte {
	var out [12]byte
	binary.LittleEndian.PutUint32(out[0:4], 2)
	binary.LittleEndian.PutUint64(out[4:12], uint64(lamports))
	return out[:]
}

func parseSystemTransferData(data []byte) (int64, error) {
	if len(data) != 12 {
		return 0, fmt.Errorf("system transfer data must be 12 bytes, got %d", len(data))
	}
	if binary.LittleEndian.Uint32(data[:4]) != 2 {
		return 0, fmt.Errorf("unsupported system instruction type %d", binary.LittleEndian.Uint32(data[:4]))
	}
	lamports := binary.LittleEndian.Uint64(data[4:])
	if lamports > uint64(^uint64(0)>>1) {
		return 0, errors.New("lamports overflow")
	}
	return int64(lamports), nil
}

func encodeCompactU16(v uint64) []byte {
	var out []byte
	for {
		elem := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			out = append(out, elem)
			break
		}
		out = append(out, elem|0x80)
	}
	return out
}

func readCompactU16(r *bytes.Reader) (uint64, error) {
	var result uint64
	var shift uint
	for i := 0; i < 3; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		result |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return result, nil
		}
		shift += 7
	}
	return 0, errors.New("compact-u16 too long")
}
