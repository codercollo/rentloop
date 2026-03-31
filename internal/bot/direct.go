// Package bot implements scheduled background workflows for the WhatsApp bot.
//
// It handles time-based tasks such as daily payment digests, querying
// unpaid units and sending summary notifications to landlords.
package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	cron "github.com/robfig/cron/v3"
)

// StartDigest registers a 6 PM EAT cron job that sends a payment summary
// to every landlord with unpaid units. Safe to call multiple times —
// only one job is registered.
//
// The cron spec "0 18 * * *" fires at 18:00 every day in the local
// timezone. Since the server runs in EAT (UTC+3), set TZ=Africa/Nairobi
// in the environment or the systemd service file.
func (s *Service) StartDigest(cron CronScheduler) error {
	_, err := cron.AddFunc("0 18 * * *", func() {
		s.runDigest()
	})
	if err != nil {
		return fmt.Errorf("digest: register cron: %w", err)
	}
	slog.Info("bot: 6 PM digest cron registered")
	return nil
}

// CronScheduler is satisfied by robfig/cron.Cron.
type CronScheduler interface {
	AddFunc(spec string, cmd func()) (cron.EntryID, error)
}

// runDigest is called by the cron scheduler every day at 6 PM EAT.
// It queries all active/grace landlords with unpaid units and sends
// each one a WhatsApp summary.
func (s *Service) runDigest() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	mk := monthKey()

	landlords, err := s.repo.GetLandlordsWithUnpaid(ctx, mk)
	if err != nil {
		slog.Error("digest: get landlords with unpaid failed", "error", err)
		return
	}

	if len(landlords) == 0 {
		slog.Info("digest: all units paid — no digests to send", "month", mk)
		return
	}

	slog.Info("digest: sending", "landlords", len(landlords), "month", mk)

	for _, l := range landlords {
		units, err := s.repo.GetUnitsWithStatus(ctx, l.ID, mk)
		if err != nil {
			slog.Error("digest: get units failed", "landlord_id", l.ID, "error", err)
			continue
		}

		var unpaid []string
		var paidCount int
		for _, u := range units {
			if u.IsPaid {
				paidCount++
			} else {
				unpaid = append(unpaid, fmt.Sprintf("• Unit %s — %s", u.Unit.UnitRef, u.Unit.TenantName))
			}
		}

		if len(unpaid) == 0 {
			continue
		}

		month := time.Now().Format("January 2006")
		sb := &strings.Builder{}
		fmt.Fprintf(sb, "*RentLoop — Daily Digest (%s)*\n", month)
		fmt.Fprintf(sb, "Paid: %d/%d units\n\n", paidCount, len(units))
		fmt.Fprintf(sb, "Still unpaid:\n%s\n\n", strings.Join(unpaid, "\n"))
		fmt.Fprintf(sb, "Reply *REMIND* to send nudges.")

		if err := s.wa.Send(ctx, l.WhatsAppPhone, sb.String()); err != nil {
			slog.Error("digest: send failed",
				"landlord_id", l.ID,
				"phone", l.WhatsAppPhone,
				"error", err,
			)
		}
	}

	slog.Info("digest: complete", "sent", len(landlords))
}
