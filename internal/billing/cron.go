package billing

import (
	"context"
	"log/slog"
	"time"

	cron "github.com/robfig/cron/v3"
)

// CronScheduler is satisfied by robfig/cron.Cron.
type CronScheduler interface {
	AddFunc(spec string, cmd func()) (cron.EntryID, error)
}

// StartCron registers two billing cron jobs:
//   - 1st of month 00:05 — move expired paid accounts to grace
//   - Daily 06:05        — move grace accounts past deadline to suspended
//
// Both jobs fire at :05 past the hour to avoid competing with the 6 PM digest.
func (s *Service) StartCron(cron CronScheduler) error {
	// 1st of month at 00:05 EAT — transition expired → grace
	if _, err := cron.AddFunc("5 0 1 * *", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := s.TransitionExpired(ctx); err != nil {
			slog.Error("billing cron: transition expired", "error", err)
		}
	}); err != nil {
		return err
	}

	// Daily at 06:05 EAT — transition overdue grace → suspended
	if _, err := cron.AddFunc("5 6 * * *", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := s.TransitionGrace(ctx); err != nil {
			slog.Error("billing cron: transition grace", "error", err)
		}
	}); err != nil {
		return err
	}

	slog.Info("billing: cron jobs registered")
	return nil
}
