package scheduler

import (
	"context"
	"log"
	"sync"
	"time"
)

type Runner interface {
	DueTaskIDs(ctx context.Context, now time.Time, limit int) ([]int64, error)
	ExecuteScheduled(ctx context.Context, taskID int64)
}

type Scheduler struct {
	runner       Runner
	scanInterval time.Duration
	workers      int
	logger       *log.Logger

	sem chan struct{}
	wg  sync.WaitGroup
}

func New(runner Runner, scanInterval time.Duration, workers int, logger *log.Logger) *Scheduler {
	if scanInterval <= 0 {
		scanInterval = time.Second
	}
	if workers <= 0 {
		workers = 4
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Scheduler{
		runner:       runner,
		scanInterval: scanInterval,
		workers:      workers,
		logger:       logger,
		sem:          make(chan struct{}, workers),
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	ticker := time.NewTicker(s.scanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.wg.Wait()
			return
		case <-ticker.C:
			ids, err := s.runner.DueTaskIDs(ctx, time.Now().UTC(), 100)
			if err != nil {
				s.logger.Printf("scheduler query due tasks failed: %v", err)
				continue
			}
			for _, id := range ids {
				s.sem <- struct{}{}
				s.wg.Add(1)
				go func(taskID int64) {
					defer func() {
						<-s.sem
						s.wg.Done()
					}()
					s.runner.ExecuteScheduled(ctx, taskID)
				}(id)
			}
		}
	}
}
