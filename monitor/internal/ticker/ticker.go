package ticker

import (
	"context"
	"sync"
	"time"

	"scutbot.cn/web/rm-monitor/pkg/logc"
)

type Ticker struct {
	interval time.Duration
	ticker   *time.Ticker
	stop     chan struct{}
	jobs     []Job
}

type Job = func(context.Context) error

func NewTicker(interval time.Duration) *Ticker {
	return &Ticker{
		interval: interval,
		ticker:   time.NewTicker(interval),
		stop:     make(chan struct{}),
	}
}

func (t *Ticker) AddJob(job Job) {
	t.jobs = append(t.jobs, job)
}

func (t *Ticker) Start() {
	for {
		select {
		case <-t.ticker.C:
			tickCtx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
			var wg sync.WaitGroup
			wg.Add(len(t.jobs))
			for _, job := range t.jobs {
				go func() {
					defer wg.Done()
					err := job(tickCtx)
					if err != nil {
						logc.Errorf(tickCtx, "job err: %v", err)
					}
				}()
			}
			wg.Wait()
			cancel()
		case <-t.stop:
			t.ticker.Stop()
			return
		}
	}
}

func (t *Ticker) Stop() {
	t.stop <- struct{}{}
	close(t.stop)
	t.ticker.Stop()
}
