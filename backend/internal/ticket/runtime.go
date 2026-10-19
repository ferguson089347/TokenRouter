package ticket

import (
	"context"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// runtime.go runs ticket expiration scans and a bounded queue of support email
// events. Reply and closure handlers enqueue events after their transactions
// commit. Separate workers keep mail latency from blocking expiration scans,
// and a full queue logs the dropped event while the saved conversation remains
// accessible in the database.
//
// Each delivery rereads module and notification switches, so disabling support
// also skips unsent mail. Reply events use the committed message ID for
// deduplication, while closure events use the ticket and event type. Shutdown
// cancels both workers and their current contexts before waiting for
// completion; pending memory-queue events are not replayed after restart.

// Ticket event names select mail templates and per-message or per-closure deduplication.
const (
	NotificationEmailEventTicketStaffReply = "ticket.staff_reply"
	NotificationEmailEventTicketCompleted  = "ticket.completed"
	NotificationEmailEventTicketCancelled  = "ticket.cancelled"
)

// TicketRuntime owns one application lifetime of ticket scanning and queued
// mail delivery.
// @project-doc docs/domains/support_tickets.md#ticket_configuration
type TicketRuntime struct {
	tickets   *TicketService          // ticket expiration operations
	config    *TicketConfigService    // current ticket configuration reader
	mail      NotificationSender      // support email delivery
	settings  FrontendSettings        // frontend address reader
	queue     chan ticketNotification // bounded committed event channel
	ctx       context.Context         // worker cancellation context
	cancel    context.CancelFunc      // worker cancellation function
	startOnce sync.Once               // single worker startup guard
	wg        sync.WaitGroup          // active worker completion barrier
}

// ticketNotification snapshots the committed event identity and recipient for
// asynchronous delivery.
type ticketNotification struct {
	ticketID  int64  // committed ticket record identifier
	messageID int64  // committed reply message identifier
	userID    int64  // recipient account identifier
	title     string // committed ticket subject
	email     string // recipient email address
	name      string // recipient display name
	event     string // support notification event name
}

// NotificationSender delivers a committed ticket event using application mail templates.
type NotificationSender interface {
	// Send delivers one event using its message or closure identity for deduplication.
	Send(context.Context, NotificationRequest) error
}

// NotificationRequest identifies a committed event and the recipient's template variables.
type NotificationRequest struct {
	Event          string            // support notification event name
	RecipientEmail string            // recipient email address
	RecipientName  string            // recipient display name
	UserID         int64             // authenticated owner identifier
	SourceType     string            // deduplication source category
	SourceID       string            // deduplication source record identifier
	Variables      map[string]string // raw template substitution values
}

// FrontendSettings supplies the configured site address for ticket notification links.
type FrontendSettings interface {
	// GetFrontendURL returns the configured browser entry address for notification links.
	GetFrontendURL(context.Context) string
}

// NewTicketRuntime prepares a bounded notification queue and cancellation
// context. Construction does not start workers; application lifecycle calls
// Start after assembly and Stop before shared settings and mail resources
// close.
func NewTicketRuntime(tickets *TicketService, config *TicketConfigService, mail NotificationSender, settings FrontendSettings) *TicketRuntime {
	ctx, cancel := context.WithCancel(context.Background())
	return &TicketRuntime{tickets: tickets, config: config, mail: mail, settings: settings, queue: make(chan ticketNotification, 100), ctx: ctx, cancel: cancel}
}

// Start launches independent expiration and notification workers once for this
// runtime. Repeated calls share the first worker pair. Expiration runs every
// minute, and mail jobs are processed sequentially from the bounded queue.
//
// Both workers select cancellation before continuing their next operation.
// Expiration and mail use child contexts with finite deadlines, and Stop
// cancels the parent then waits for both workers. This runtime is intended for
// one application lifetime and cannot restart after its context has been
// cancelled.
func (r *TicketRuntime) Start() {
	r.startOnce.Do(func() {
		r.wg.Add(2)
		go func() {
			defer r.wg.Done()
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-r.ctx.Done():
					return
				case <-ticker.C:
					r.expire()
				}
			}
		}()
		go func() {
			defer r.wg.Done()
			for {
				select {
				case <-r.ctx.Done():
					return
				case job := <-r.queue:
					if r.ctx.Err() != nil {
						return
					}
					r.send(job)
				}
			}
		}()
	})
}

// Stop cancels ticket workers and waits for current work to finish. Repeated
// calls are safe, queued events may be discarded, and the runtime cannot be
// restarted after stopping.
func (r *TicketRuntime) Stop() {
	if r != nil {
		r.cancel()
		r.wg.Wait()
	}
}

// expire runs one expiration scan with a twenty-second child context. Shutdown
// cancels the scan; other failures are logged, and a successful update logs
// the number of tickets expired.
func (r *TicketRuntime) expire() {
	ctx, cancel := context.WithTimeout(r.ctx, 20*time.Second)
	defer cancel()
	count, err := r.tickets.Expire(ctx)
	if err != nil && r.ctx.Err() == nil {
		slog.Error("ticket expiry scan failed", "error", err)
	} else if count > 0 {
		slog.Info("waiting tickets expired", "count", count)
	}
}

// NotifyReply enqueues a newly committed staff reply identified by message ID.
// Replayed requests have no creation marker and are skipped; queue admission
// failures leave the committed message intact.
func (r *TicketRuntime) NotifyReply(ticket *Ticket) {
	if ticket == nil || !ticket.ReplyCreated || ticket.ReplyMessageID <= 0 {
		return
	}
	r.enqueue(ticketNotification{ticketID: ticket.ID, messageID: ticket.ReplyMessageID, userID: ticket.UserID, title: ticket.Title, email: ticket.UserEmail, name: ticket.UserName, event: NotificationEmailEventTicketStaffReply})
}

// NotifyClosure enqueues the first committed administrator completion or
// withdrawal. Personal actions, expiration, and terminal-state replays are
// skipped so the notification describes a newly committed support action only.
func (r *TicketRuntime) NotifyClosure(ticket *Ticket) {
	if ticket == nil || !ticket.ClosureCreated || ticket.ClosedByRole != "admin" {
		return
	}
	var event string
	switch ticket.Status {
	case TicketStatusCompleted:
		event = NotificationEmailEventTicketCompleted
	case TicketStatusCancelled:
		event = NotificationEmailEventTicketCancelled
	default:
		return
	}
	r.enqueue(ticketNotification{ticketID: ticket.ID, userID: ticket.UserID, title: ticket.Title, email: ticket.UserEmail, name: ticket.UserName, event: event})
}

// enqueue admits a committed event when the runtime is active and queue space
// exists. Shutdown or capacity exhaustion logs the event identity and returns
// immediately to the request handler.
func (r *TicketRuntime) enqueue(job ticketNotification) {
	select {
	case <-r.ctx.Done():
		slog.Warn("ticket notification skipped during shutdown", "ticket_id", job.ticketID, "message_id", job.messageID, "event", job.event)
		return
	default:
	}
	select {
	case r.queue <- job:
	default:
		slog.Error("ticket notification queue full", "ticket_id", job.ticketID, "message_id", job.messageID, "event", job.event)
	}
}

// send checks current notification settings and delivers one committed support
// event. It uses a thirty-second child context so shutdown can cancel settings
// and SMTP work. Disabled modules or disabled support mail skip the queued
// event.
//
// Replies identify their source by message ID, allowing different replies in
// one ticket to send independently. Closure events identify their source by
// ticket ID and event type. The notification module owns template
// localization, recipient preferences, and persisted deduplication; delivery
// errors are logged after the business transaction has already committed.
func (r *TicketRuntime) send(job ticketNotification) {
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	cfg, err := r.config.Get(ctx)
	if err != nil {
		slog.Error("ticket notification settings failed", "ticket_id", job.ticketID, "message_id", job.messageID, "event", job.event, "error", err)
		return
	}
	// Recheck switches at delivery time because queued jobs may predate an
	// administrator disabling support or its email notifications.
	if !cfg.Enabled || !cfg.NotifyOnStaffReply {
		slog.Info("ticket notification disabled by settings", "ticket_id", job.ticketID, "message_id", job.messageID, "event", job.event)
		return
	}
	// Each reply uses its message identifier, while closure uses the ticket and
	// event, allowing distinct support actions to deliver independently.
	sourceType, sourceID := "support_ticket", strconv.FormatInt(job.ticketID, 10)
	if job.event == NotificationEmailEventTicketStaffReply {
		sourceType, sourceID = "support_ticket_message", strconv.FormatInt(job.messageID, 10)
	}
	link := ""
	if r.settings != nil {
		link = ticketNotificationURL(r.settings.GetFrontendURL(ctx), job.ticketID)
	}
	// An unconfigured site address leaves the link empty; the committed ticket
	// number still lets recipients find their conversation after signing in.
	err = r.mail.Send(ctx, NotificationRequest{Event: job.event, RecipientEmail: job.email, RecipientName: job.name, UserID: job.userID, SourceType: sourceType, SourceID: sourceID, Variables: map[string]string{"ticket_id": strconv.FormatInt(job.ticketID, 10), "ticket_title": job.title, "ticket_url": link}})
	if err != nil {
		slog.Error("ticket notification failed", "ticket_id", job.ticketID, "message_id", job.messageID, "event", job.event, "error", err)
	}
}

// ticketNotificationURL appends an owned ticket path to a configured HTTP site
// URL. Invalid addresses return empty, and base queries and fragments are
// cleared so they cannot obscure the destination.
func ticketNotificationURL(base string, ticketID int64) string {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/tickets/" + strconv.FormatInt(ticketID, 10)
	parsed.RawPath, parsed.RawQuery, parsed.Fragment, parsed.RawFragment = "", "", "", ""
	parsed.ForceQuery = false
	return parsed.String()
}
