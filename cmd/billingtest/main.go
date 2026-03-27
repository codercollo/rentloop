// cmd/billingtest/main.go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/codercollo/rentloop/internal/billing"
	"github.com/codercollo/rentloop/internal/db"
)

// noopNotifier satisfies billing.Notifier without sending anything.
type noopNotifier struct{}

func (n *noopNotifier) Send(_ context.Context, to, msg string) error {
	slog.Info("billing notify (noop)", "to", to, "msg", msg)
	return nil
}

func main() {
	pool, err := db.Connect(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Println("db connect error:", err)
		os.Exit(1)
	}

	repo := billing.NewRepository(pool)
	svc := billing.NewService(repo, &noopNotifier{}, 5, 50, 10)

	if err := svc.TransitionGrace(context.Background()); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
	fmt.Println("done")
}
