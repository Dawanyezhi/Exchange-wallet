package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type solanaRPCClient struct {
	URL    string
	Client *http.Client
}

func (c solanaRPCClient) GetBlock(ctx context.Context, slot uint64) ([]byte, error) {
	params := []interface{}{
		slot,
		map[string]interface{}{
			"encoding":                       "jsonParsed",
			"transactionDetails":             "full",
			"rewards":                        false,
			"maxSupportedTransactionVersion": 0,
		},
	}
	return c.call(ctx, "getBlock", params)
}

func (c solanaRPCClient) GetTransaction(ctx context.Context, signature string) ([]byte, error) {
	params := []interface{}{
		signature,
		map[string]interface{}{
			"encoding":                       "jsonParsed",
			"maxSupportedTransactionVersion": 0,
		},
	}
	return c.call(ctx, "getTransaction", params)
}

func (c solanaRPCClient) call(ctx context.Context, method string, params []interface{}) ([]byte, error) {
	if c.Client == nil {
		c.Client = &http.Client{Timeout: 20 * time.Second}
	}
	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "exchange-wallet-sol-demo",
		"method":  method,
		"params":  params,
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("solana rpc status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(rawResp)))
	}
	var decoded struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(rawResp, &decoded); err != nil {
		return nil, err
	}
	if len(decoded.Error) > 0 && string(decoded.Error) != "null" {
		return nil, fmt.Errorf("solana rpc error: %s", string(decoded.Error))
	}
	if len(decoded.Result) == 0 || string(decoded.Result) == "null" {
		return nil, fmt.Errorf("solana rpc returned null result for %s", method)
	}
	return decoded.Result, nil
}
