// Package alarm 提供告警基础库。
// 当前为 Stub 实现，接口完整，生产中替换为实际的 Lark/钉钉/PagerDuty 实现。
package alarm

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
)

// Alarm 告警接口。所有告警实现必须满足此接口，便于测试时 mock。
type Alarm interface {
	// Send 发送告警消息。
	Send(ctx context.Context, title, message string) error
	// DeepReorgAlert 深度重组专用告警（Front==Back 时触发，需要人工介入）。
	DeepReorgAlert(height int64) error
	// BalanceMismatchAlert 余额对账异常告警。
	BalanceMismatchAlert(chain, symbol string, onChain, offChain string) error
}

// LarkAlarm 基于 Lark（飞书）Webhook 的告警实现（Stub）。
// 生产中应替换为真实的 HTTP 调用。
type LarkAlarm struct {
	webhookURL string
	logger     *logrus.Logger
}

// NewLarkAlarm 创建 Lark 告警实例。
func NewLarkAlarm(webhookURL string, logger *logrus.Logger) *LarkAlarm {
	if logger == nil {
		logger = logrus.New()
	}
	return &LarkAlarm{
		webhookURL: webhookURL,
		logger:     logger,
	}
}

// Send 发送 Lark 告警（当前为 Stub，仅打印日志）。
func (a *LarkAlarm) Send(_ context.Context, title, message string) error {
	// TODO: 生产中实现真实的 HTTP POST 到 Lark Webhook
	// payload := map[string]interface{}{
	//     "msg_type": "text",
	//     "content": map[string]string{"text": fmt.Sprintf("[%s]\n%s", title, message)},
	// }
	a.logger.WithFields(logrus.Fields{
		"alarm_title":   title,
		"alarm_message": message,
		"webhook":       a.webhookURL,
	}).Warn("ALARM (stub)")
	return nil
}

// DeepReorgAlert 深度重组告警。
func (a *LarkAlarm) DeepReorgAlert(height int64) error {
	return a.Send(context.Background(),
		"[CRITICAL] 深度区块重组",
		fmt.Sprintf("Front == Back == %d，已超出自动回滚范围，需要人工介入！", height),
	)
}

// BalanceMismatchAlert 余额对账异常告警。
func (a *LarkAlarm) BalanceMismatchAlert(chain, symbol, onChain, offChain string) error {
	return a.Send(context.Background(),
		"[WARNING] 余额对账异常",
		fmt.Sprintf("链=%s 代币=%s 链上余额=%s 链下余额=%s", chain, symbol, onChain, offChain),
	)
}

// NoopAlarm 空实现，用于测试。
type NoopAlarm struct{}

func (n *NoopAlarm) Send(_ context.Context, _, _ string) error    { return nil }
func (n *NoopAlarm) DeepReorgAlert(_ int64) error                  { return nil }
func (n *NoopAlarm) BalanceMismatchAlert(_, _, _, _ string) error  { return nil }
