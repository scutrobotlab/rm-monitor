package utils

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/pkg/errors"
)

type LarkRetryFunc func(chatID string, f func() error) error

func CreateCardEntity(ctx context.Context, client *lark.Client, retry LarkRetryFunc, content *MatchCardContent) (string, map[string]any, error) {
	contentData, payload, err := CardEntityData(content)
	if err != nil {
		return "", nil, err
	}
	var resp *larkcardkit.CreateCardResp
	err = retry("", func() error {
		var callErr error
		resp, callErr = client.Cardkit.V1.Card.Create(ctx, larkcardkit.NewCreateCardReqBuilder().
			Body(larkcardkit.NewCreateCardReqBodyBuilder().
				Type("card_json").
				Data(contentData).
				Build()).
			Build())
		if callErr != nil {
			return callErr
		}
		if !resp.Success() {
			return resp
		}
		return nil
	})
	if err != nil {
		return "", nil, errors.Wrap(err, "create cardkit card")
	}
	if resp.Data == nil || resp.Data.CardId == nil || *resp.Data.CardId == "" {
		return "", nil, errors.New("create cardkit card returned empty card_id")
	}
	return *resp.Data.CardId, payload, nil
}

func UpdateCardEntity(ctx context.Context, client *lark.Client, retry LarkRetryFunc, cardID, uuid string, sequence int64, content *MatchCardContent) (map[string]any, error) {
	contentData, payload, err := CardEntityData(content)
	if err != nil {
		return nil, err
	}
	req := larkcardkit.NewUpdateCardReqBuilder().
		CardId(cardID).
		Body(larkcardkit.NewUpdateCardReqBodyBuilder().
			Card(larkcardkit.NewCardBuilder().
				Type("card_json").
				Data(contentData).
				Build()).
			Uuid(uuid).
			Sequence(int(sequence)).
			Build()).
		Build()
	var resp *larkcardkit.UpdateCardResp
	err = retry("", func() error {
		var callErr error
		resp, callErr = client.Cardkit.V1.Card.Update(ctx, req)
		if callErr != nil {
			return callErr
		}
		if !resp.Success() {
			return resp
		}
		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "update cardkit card")
	}
	return payload, nil
}

func IsCardUpdateAlreadyApplied(err error) bool {
	var codeErr *larkcore.CodeError
	if stderrors.As(err, &codeErr) {
		return codeErr.Code == 200770 || codeErr.Code == 300317
	}
	var valueCodeErr larkcore.CodeError
	if stderrors.As(err, &valueCodeErr) {
		return valueCodeErr.Code == 200770 || valueCodeErr.Code == 300317
	}
	return false
}

func SendCardReferenceMessage(ctx context.Context, client *lark.Client, retry LarkRetryFunc, chatID, cardID, uuid string) (string, error) {
	contentData, err := CardReferenceMessageContent(cardID)
	if err != nil {
		return "", err
	}
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.ReceiveIdTypeChatId).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType(larkim.MsgTypeInteractive).
			Content(contentData).
			Uuid(uuid).
			Build()).
		Build()
	var resp *larkim.CreateMessageResp
	err = retry(chatID, func() error {
		var callErr error
		resp, callErr = client.Im.V1.Message.Create(ctx, req)
		if callErr != nil {
			return callErr
		}
		if !resp.Success() {
			return resp
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if resp.Data == nil || resp.Data.MessageId == nil || *resp.Data.MessageId == "" {
		return "", errors.New("create lark message returned empty message_id")
	}
	return *resp.Data.MessageId, nil
}

func PatchCardReferenceMessage(ctx context.Context, client *lark.Client, retry LarkRetryFunc, messageID, cardID string) error {
	contentData, err := CardReferenceMessageContent(cardID)
	if err != nil {
		return err
	}
	req := larkim.NewPatchMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().Content(contentData).Build()).
		Build()
	var resp *larkim.PatchMessageResp
	return retry("", func() error {
		var callErr error
		resp, callErr = client.Im.V1.Message.Patch(ctx, req)
		if callErr != nil {
			return callErr
		}
		if !resp.Success() {
			return resp
		}
		return nil
	})
}

func CardEntityData(content *MatchCardContent) (string, map[string]any, error) {
	contentData, err := content.RenderJSON()
	if err != nil {
		return "", nil, err
	}
	return contentData, ToMap(content), nil
}

func CardReferenceMessageContent(cardID string) (string, error) {
	b, err := json.Marshal(map[string]any{
		"type": "card",
		"data": map[string]any{
			"card_id": cardID,
		},
	})
	if err != nil {
		return "", errors.Wrap(err, "marshal cardkit message content")
	}
	return string(b), nil
}

func SmokeUpdateUUID() string {
	return "card-smoke-update:" + time.Now().Format("150405.000000000")
}

func ToMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}
