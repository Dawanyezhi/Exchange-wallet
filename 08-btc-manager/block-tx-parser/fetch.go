package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type bitcoinRPCClient struct {
	URL      string
	User     string
	Password string
	Client   *http.Client
}

func FetchBlockHexFromBlockstream(ctx context.Context, blockHash string) (string, error) {
	url := "https://blockstream.info/api/block/" + blockHash + "/raw"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("blockstream status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", raw), nil
}

func (c bitcoinRPCClient) GetBlockHex(ctx context.Context, blockHash string) (string, error) {
	if c.Client == nil {
		c.Client = &http.Client{Timeout: 15 * time.Second}
	}
	body := map[string]any{
		"jsonrpc": "1.0",
		"id":      "exchange-wallet-btc-demo",
		"method":  "getblock",
		"params":  []any{blockHash, 0},
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.User != "" || c.Password != "" {
		token := base64.StdEncoding.EncodeToString([]byte(c.User + ":" + c.Password))
		req.Header.Set("Authorization", "Basic "+token)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bitcoin rpc status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(rawResp)))
	}
	var decoded struct {
		Result string          `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(rawResp, &decoded); err != nil {
		return "", err
	}
	if len(decoded.Error) > 0 && string(decoded.Error) != "null" {
		return "", fmt.Errorf("bitcoin rpc error: %s", string(decoded.Error))
	}
	return decoded.Result, nil
}
