package recording

import (
	"context"

	"github.com/pkg/errors"
	"github.com/samber/lo"
	"github.com/tidwall/gjson"
	"resty.dev/v3"
)

const DefaultLiveInfoURL = "https://rm-static.djicdn.com/live_json/live_game_info.json"

func LiveURLs(ctx context.Context, client *resty.Client, liveInfoURL, zone, res string) (map[string]string, error) {
	if liveInfoURL == "" {
		liveInfoURL = DefaultLiveInfoURL
	}
	resp, err := client.R().SetContext(ctx).Get(liveInfoURL)
	if err != nil {
		return nil, errors.Wrap(err, "get live info")
	}
	if !resp.IsSuccess() {
		return nil, errors.Errorf("get live info status %d", resp.StatusCode())
	}
	info, found := lo.Find(gjson.GetBytes(resp.Bytes(), "eventData").Array(), func(item gjson.Result) bool {
		return item.Get("zoneName").String() == zone
	})
	if !found {
		return nil, errors.New("live info for zone " + zone + " not found")
	}

	urls := lo.FilterSliceToMap(info.Get("fpvData").Array(), func(item gjson.Result) (string, string, bool) {
		source, found := lo.Find(item.Get("sources").Array(), func(item gjson.Result) bool {
			return item.Get("res").String() == res
		})
		if !found {
			return "", "", false
		}
		return item.Get("role").String(), source.Get("src").String(), true
	})
	mainURL, found := lo.Find(info.Get("zoneLiveString").Array(), func(item gjson.Result) bool {
		return item.Get("res").String() == res
	})
	if found {
		urls["主视角"] = mainURL.Get("src").String()
	}
	return urls, nil
}
