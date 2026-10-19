package app

import (
	"context"
	"database/sql"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/ticket"
	"github.com/TokenFlux/TokenRouter/internal/ticket/postgres"
)

// ticket.go assembles personal support tickets with shared database, settings, and
// email delivery resources. Ticket persistence owns user isolation and reply
// transactions, while lifecycle hooks start expiration scans and stop their work
// before application resources close.

// ticketMail converts committed ticket events into notification template requests.
type ticketMail struct {
	mail *notification.NotificationEmailService // shared notification delivery
}

// Send forwards one ticket event with its persistent deduplication identity.
func (m ticketMail) Send(ctx context.Context, in ticket.NotificationRequest) error {
	return m.mail.Send(ctx, notification.SendRequest{
		Event:          in.Event,
		RecipientEmail: in.RecipientEmail,
		RecipientName:  in.RecipientName,
		UserID:         in.UserID,
		SourceType:     in.SourceType,
		SourceID:       in.SourceID,
		Variables:      in.Variables,
	})
}

// provideTicketConfig connects ticket configuration persistence and public settings publication.
func provideTicketConfig(store *settings.Store) *ticket.TicketConfigService {
	cfg := ticket.NewTicketConfigService(store)
	cfg.SetOnUpdate(store.NotifyUpdated)
	return cfg
}

// provideTickets constructs ticket operations using the application's shared SQL connection.
func provideTickets(db *sql.DB, cfg *ticket.TicketConfigService) *ticket.TicketService {
	return ticket.NewTicketService(postgres.NewTicketRepository(db), cfg)
}

// provideTicketRuntime registers support notifications and expiration with application lifecycle.
// The manager starts producers after assembly and cancels them before shared
// resources close. Stop waits for cancellation-aware SQL and mail operations to finish.
func provideTicketRuntime(service *ticket.TicketService, cfg *ticket.TicketConfigService, mail *notification.NotificationEmailService, display *site.DisplaySettings, manager *lifecycle.Manager) *ticket.TicketRuntime {
	runtime := ticket.NewTicketRuntime(service, cfg, ticketMail{mail}, display)
	manager.Register(lifecycle.Hook{
		Name:       "TicketRuntime",
		StartOrder: 980,
		StopOrder:  20,
		Start: func(context.Context) error {
			runtime.Start()
			return nil
		},
		Stop: func(context.Context) error {
			runtime.Stop()
			return nil
		},
	})
	return runtime
}
