package logic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"scutbot.cn/web/rm-monitor/lark-notifier/internal/utils"
)

const (
	resultWebhookSendingTTLSeconds = 10 * 60
	resultWebhookSentTTLSeconds    = 30 * 24 * 60 * 60
)

type resultWebhookPayload struct {
	MsgType string                  `json:"msg_type"`
	Card    *utils.MatchCardContent `json:"card"`
}

type resultWebhookResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func (l *NotifyLogic) pushResultWebhooks(matchID string, content *utils.MatchCardContent) {
	if content == nil || len(l.svcCtx.Config.ResultWebhookURLs) == 0 {
		return
	}
	payload := resultWebhookPayload{
		MsgType: "interactive",
		Card:    content,
	}
	for _, rawURL := range l.svcCtx.Config.ResultWebhookURLs {
		webhookURL := strings.TrimSpace(rawURL)
		if webhookURL == "" {
			continue
		}
		hash := resultWebhookHash(webhookURL)
		key := resultWebhookKey(matchID, hash)
		claimed, err := l.svcCtx.RedisClient.SetNXCtx(l.ctx, key, "sending", resultWebhookSendingTTLSeconds)
		if err != nil {
			l.Error(errors.Wrapf(err, "claim result webhook %s", hash))
			continue
		}
		if !claimed {
			continue
		}
		if err := l.postResultWebhook(webhookURL, payload); err != nil {
			_ = l.svcCtx.RedisClient.DelCtx(l.ctx, key)
			l.Error(errors.Wrapf(err, "post result webhook %s", hash))
			continue
		}
		if err := l.svcCtx.RedisClient.SetexCtx(l.ctx, key, "sent", resultWebhookSentTTLSeconds); err != nil {
			l.Error(errors.Wrapf(err, "mark result webhook sent %s", hash))
		}
	}
}

func (l *NotifyLogic) postResultWebhook(webhookURL string, payload resultWebhookPayload) error {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := l.svcCtx.RestyClient.R().
			SetContext(l.ctx).
			SetHeader("Content-Type", "application/json").
			SetBody(payload).
			Post(webhookURL)
		if err != nil {
			last = fmt.Errorf("request failed: %T", err)
		} else if resp.IsError() {
			last = fmt.Errorf("webhook returned status %d", resp.StatusCode())
		} else {
			var body resultWebhookResponse
			if err := json.Unmarshal(resp.Bytes(), &body); err == nil && body.Code != 0 {
				last = fmt.Errorf("webhook returned code %d: %s", body.Code, body.Msg)
			} else {
				return nil
			}
		}
		if attempt < 2 {
			wait := retryDelay(attempt)
			select {
			case <-l.ctx.Done():
				return l.ctx.Err()
			case <-time.After(wait):
			}
		}
	}
	return last
}

func resultWebhookHash(webhookURL string) string {
	sum := sha256.Sum256([]byte(webhookURL))
	return hex.EncodeToString(sum[:])[:16]
}

func resultWebhookKey(matchID, webhookHash string) string {
	return fmt.Sprintf("rm-monitor:lark-result-webhook:%s:%s", matchID, webhookHash)
}
