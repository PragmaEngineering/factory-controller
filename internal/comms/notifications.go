package comms

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

type notifier struct {
	mu      sync.Mutex
	changed chan struct{}
}

func (s *Service) wakeChannel() <-chan struct{} {
	s.notifications.mu.Lock()
	defer s.notifications.mu.Unlock()
	return s.notifications.changed
}
func (s *Service) listen(ctx context.Context) {
	for ctx.Err() == nil {
		conn, err := pgx.ConnectConfig(ctx, s.DB.Pool.Config().ConnConfig.Copy())
		if err == nil {
			_, err = conn.Exec(ctx, "LISTEN factory_comms")
			for err == nil {
				_, err = conn.WaitForNotification(ctx)
				if err == nil {
					s.notifications.mu.Lock()
					close(s.notifications.changed)
					s.notifications.changed = make(chan struct{})
					s.notifications.mu.Unlock()
				}
			}
			_ = conn.Close(context.Background())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
