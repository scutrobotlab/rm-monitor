package record

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/samber/lo"
	"github.com/tidwall/gjson"
	"github.com/zeromicro/go-queue/natsq"
	"github.com/zeromicro/go-zero/core/logx"

	"resty.dev/v3"
	"scutbot.cn/web/rm-monitor/monitor/types"
)

const liveInfoUrl = "https://rm-static.djicdn.com/live_json/live_game_info.json"

type Daemon struct {
	res         string
	baseDir     string
	restyClient *resty.Client
	pusher      *natsq.DefaultProducer

	tasks map[string]*Task
	locks map[string]*sync.Mutex
	logx.Logger
}

func NewDaemon(res, baseDir string, restyClient resty.Client, pusher *natsq.DefaultProducer) *Daemon {
	return &Daemon{
		res:         res,
		baseDir:     baseDir,
		restyClient: &restyClient,
		pusher:      pusher,
		tasks:       make(map[string]*Task),
		locks:       make(map[string]*sync.Mutex),
		Logger:      logx.WithContext(context.Background()),
	}
}

func (d *Daemon) Close() error {
	for _, task := range d.tasks {
		task.Stop()
	}

	return nil
}

func (d *Daemon) StartBatch(ctx context.Context, m *types.Match) error {
	namespace := fmt.Sprintf("%d. %s[%s] VS %s[%s]",
		m.Order, m.RedTeam.SchoolName, m.RedTeam.Name, m.BlueTeam.SchoolName, m.BlueTeam.Name)
	zone := m.ZoneName
	round := fmt.Sprintf("Round %d", m.Round())

	if _, ok := d.locks[namespace]; !ok {
		d.locks[namespace] = &sync.Mutex{}
	}

	if !d.locks[namespace].TryLock() {
		return errors.New("task already running")
	}

	resp, err := d.restyClient.R().
		SetContext(ctx).
		Get(liveInfoUrl)
	if err != nil {
		return errors.Wrap(err, "failed to get live info")
	}

	info, found := lo.Find(gjson.GetBytes(resp.Bytes(), "eventData").Array(), func(item gjson.Result) bool {
		return item.Get("zoneName").String() == zone
	})
	if !found {
		return errors.New("live info for zone " + zone + " not found")
	}

	urls := lo.FilterSliceToMap(info.Get("fpvData").Array(), func(item gjson.Result) (string, string, bool) {
		source, found := lo.Find(item.Get("sources").Array(), func(item gjson.Result) bool {
			return item.Get("res").String() == d.res
		})
		if !found {
			return "", "", false
		}

		return item.Get("role").String(), source.Get("src").String(), true
	})

	mainUrl, found := lo.Find(info.Get("zoneLiveString").Array(), func(item gjson.Result) bool {
		return item.Get("res").String() == d.res
	})
	if found {
		urls["主视角"] = mainUrl.Get("src").String()
	}

	if len(urls) == 0 {
		d.locks[namespace].Unlock()
		return errors.New("no live urls found")
	}

	for role, url := range urls {
		output := path.Join(zone, namespace, round, fmt.Sprintf("%s_%d", role, time.Now().UnixMilli()))
		name := fmt.Sprintf("%s:%s:%s", zone, namespace, role)

		if err := d.StartTask(name, url, output, role, m); err != nil {
			return errors.Wrapf(err, "failed to start task %s", name)
		}
	}

	return nil
}

func (d *Daemon) StopBatch(m *types.Match) error {
	namespace := fmt.Sprintf("%d. %s[%s] VS %s[%s]",
		m.Order, m.RedTeam.SchoolName, m.RedTeam.Name, m.BlueTeam.SchoolName, m.BlueTeam.Name)
	zone := m.ZoneName

	names := lo.Filter(lo.Keys(d.tasks), func(item string, index int) bool {
		return strings.HasPrefix(item, fmt.Sprintf("%s:%s", zone, namespace))
	})

	for _, name := range names {
		if err := d.StopTask(name); err != nil {
			return errors.Wrapf(err, "failed to stop task %s", name)
		}
	}

	if lock, ok := d.locks[namespace]; ok {
		lock.Unlock()
	}

	return nil
}

func (d *Daemon) StartTask(name, url, output, role string, m *types.Match) error {
	if _, ok := d.tasks[name]; ok {
		return errors.New("task already exists")
	}

	task := NewTask(name, url, d.baseDir, role, m, d.pusher)
	d.tasks[name] = task

	go func() {
		if err := task.Start(output); err != nil {
			d.Error(errors.Wrapf(err, "failed to start task %s", name))
		}
	}()

	return nil
}

func (d *Daemon) StopTask(name string) error {
	if task, ok := d.tasks[name]; ok {
		task.Stop()
		delete(d.tasks, name)
		return nil
	}

	return errors.New("task not found")
}
