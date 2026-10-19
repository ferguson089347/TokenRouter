package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	support "github.com/TokenFlux/TokenRouter/internal/ticket"
)

// repository.go stores tickets, messages, and private attachments in
// PostgreSQL transactions. User entry points filter by the authenticated
// owner, while administrator entry points permit support access across owners.
// Payment references are checked against the creating user, and response
// queries select a limited payment summary.
//
// Creation locks the user before checking the open-ticket count. Replies and
// closure changes lock the ticket before checking state or idempotency,
// keeping concurrent requests consistent across application instances.
// Messages and their file bytes commit together. Detail reads use one
// repeatable-read snapshot, and expiration performs a conditional update that
// rechecks waiting state after competing row locks are released.

const (
	ticketColumns = `t.id, t.user_id, u.email, COALESCE(u.username, ''), t.type, t.title, t.content,
 t.priority, t.status, t.order_id,
 t.created_at, t.updated_at, t.last_staff_reply_at, t.closed_at, t.closed_by, t.closed_by_role,
 o.id, COALESCE(o.out_trade_no, ''), COALESCE(o.amount, 0), COALESCE(o.pay_amount, 0),
 COALESCE(NULLIF(o.provider_snapshot->>'currency', ''), 'CNY'), COALESCE(o.status, '')`

	ticketJoins = ` FROM support_tickets t JOIN users u ON u.id = t.user_id
 LEFT JOIN payment_orders o ON o.id = t.order_id`
)

// ticketRepository implements ticket persistence with a shared application SQL
// connection pool.
type ticketRepository struct{ db *sql.DB }

// ticketQuerier supplies row queries shared by SQL pools and ticket
// transactions.
type ticketQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// ticketScanner accepts the joined ticket columns from rows or a single row.
type ticketScanner interface{ Scan(...any) error }

// NewTicketRepository uses the application SQL pool for transactional support-
// ticket persistence.
func NewTicketRepository(db *sql.DB) support.TicketRepository { return &ticketRepository{db: db} }

// scanTicket maps a joined ticket row and nullable payment summary into domain
// values. Missing rows return the ticket not-found error, and absent payment
// rows leave the summary nil.
func scanTicket(row ticketScanner) (*support.Ticket, error) {
	ticket := &support.Ticket{}
	order := &support.TicketOrder{}
	var orderID sql.NullInt64
	err := row.Scan(&ticket.ID, &ticket.UserID, &ticket.UserEmail, &ticket.UserName, &ticket.Type, &ticket.Title, &ticket.Content,
		&ticket.Priority, &ticket.Status, &ticket.OrderID,
		&ticket.CreatedAt, &ticket.UpdatedAt, &ticket.LastStaffReplyAt, &ticket.ClosedAt, &ticket.ClosedBy, &ticket.ClosedByRole,
		&orderID, &order.OutTradeNo, &order.Amount, &order.PayAmount, &order.Currency, &order.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, support.ErrTicketNotFound
	}
	if err != nil {
		return nil, err
	}
	if orderID.Valid {
		order.ID = orderID.Int64
		ticket.Order = order
	}
	return ticket, nil
}

// ticketConditions builds bound SQL predicates for actor visibility and list
// filters. Search escapes LIKE metacharacters, while administrator searches
// additionally include the owner email and username fields.
func ticketConditions(actor support.TicketActor, filter support.TicketListFilter) (string, []any) {
	conditions := []string{"1=1"}
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(clause, len(args)))
	}
	if !actor.IsAdmin {
		add("t.user_id = $%d", actor.UserID)
	}
	if filter.Status != "" {
		add("t.status = $%d", filter.Status)
	}
	if filter.Type != "" {
		add("t.type = $%d", filter.Type)
	}
	if filter.Priority != "" {
		add("t.priority = $%d", filter.Priority)
	}
	if filter.Search != "" {
		// Escape wildcard characters before binding the search term so a literal user
		// query cannot broaden the ticket matches through SQL pattern syntax.
		search := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(filter.Search)
		args = append(args, "%"+search+"%")
		n := len(args)
		clause := fmt.Sprintf("(t.title ILIKE $%d OR CAST(t.id AS TEXT) ILIKE $%d", n, n)
		if actor.IsAdmin {
			clause += fmt.Sprintf(" OR u.email ILIKE $%d OR u.username ILIKE $%d", n, n)
		}
		conditions = append(conditions, clause+")")
	}
	return strings.Join(conditions, " AND "), args
}

// List returns a bounded ticket page using caller-normalized pagination and
// filters. Administrators receive priority ordering before recent activity;
// personal pages sort by recent activity, and both use stable identifier tie-
// breaking.
func (r *ticketRepository) List(ctx context.Context, actor support.TicketActor, filter support.TicketListFilter) (*support.TicketListResult, error) {
	where, args := ticketConditions(actor, filter)
	result := &support.TicketListResult{Items: []support.Ticket{}, Page: filter.Page, PageSize: filter.PageSize}
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*)"+ticketJoins+" WHERE "+where, args...).Scan(&result.Total); err != nil {
		return nil, err
	}
	result.Pages = int((result.Total + int64(filter.PageSize) - 1) / int64(filter.PageSize))
	order := "t.updated_at DESC, t.id DESC"
	if actor.IsAdmin {
		order = "CASE t.priority WHEN 'high' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END, " + order
	}
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, "SELECT "+ticketColumns+ticketJoins+" WHERE "+where+" ORDER BY "+order+fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	// Iteration errors are checked through Rows.Err; deferred close releases
	// the read cursor if decoding returns before exhausting all selected records.
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		ticket, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		result.Items = append(result.Items, *ticket)
	}
	return result, rows.Err()
}

// Get reads ticket metadata, messages, and attachment metadata from one
// repeatable-read snapshot. The actor determines whether ownership limits the
// selected ticket. Attachment bytes are obtained through the separately
// authorized download method.
//
// The read transaction stays open until all conversation and attachment
// metadata has been assembled. Concurrent replies therefore appear together
// with their files in a later detail request, rather than splitting one
// response across database snapshots. Any read or commit failure returns an
// error, with deferred rollback releasing unfinished work.
func (r *ticketRepository) Get(ctx context.Context, actor support.TicketActor, id int64) (*support.Ticket, error) {
	// Keep all detail queries inside one snapshot so a concurrent reply cannot
	// appear with only part of its message and attachment metadata.
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	// Rollback releases unfinished writes on every failure path; after a
	// successful commit, the deferred call returns the expected completed-transaction error.
	defer func() { _ = tx.Rollback() }()
	ticket, err := getTicket(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ticket, nil
}

// getTicket assembles a visible ticket and its ordered conversation using the
// supplied query session. Callers provide a transaction when a consistent
// detail snapshot is required; file bytes remain separate.
func getTicket(ctx context.Context, db ticketQuerier, actor support.TicketActor, id int64) (*support.Ticket, error) {
	ticket, err := scanTicket(db.QueryRowContext(ctx, "SELECT "+ticketColumns+ticketJoins+" WHERE t.id = $1 AND ($2 OR t.user_id = $3)", id, actor.IsAdmin, actor.UserID))
	if err != nil {
		return nil, err
	}
	ticket.Messages = []support.TicketMessage{}
	rows, err := db.QueryContext(ctx, `SELECT m.id, m.ticket_id, m.user_id, COALESCE(NULLIF(u.username, ''), CASE WHEN m.is_staff THEN '客服' ELSE '用户' END), m.is_staff, m.content, m.created_at
 FROM support_ticket_messages m JOIN users u ON u.id = m.user_id WHERE m.ticket_id = $1 ORDER BY m.id`, id)
	if err != nil {
		return nil, err
	}
	indices := map[int64]int{}
	for rows.Next() {
		var msg support.TicketMessage
		msg.Attachments = []support.TicketAttachment{}
		if err := rows.Scan(&msg.ID, &msg.TicketID, &msg.UserID, &msg.SenderName, &msg.IsStaff, &msg.Content, &msg.CreatedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		indices[msg.ID] = len(ticket.Messages)
		ticket.Messages = append(ticket.Messages, msg)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	rows, err = db.QueryContext(ctx, `SELECT a.id, a.message_id, a.filename, a.content_type, a.size, a.created_at
 FROM support_ticket_attachments a JOIN support_ticket_messages m ON m.id = a.message_id WHERE m.ticket_id = $1 ORDER BY a.id`, id)
	if err != nil {
		return nil, err
	}
	// Iteration errors are checked through Rows.Err; deferred close releases
	// the read cursor if decoding returns before exhausting all selected records.
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var a support.TicketAttachment
		if err := rows.Scan(&a.ID, &a.MessageID, &a.Filename, &a.ContentType, &a.Size, &a.CreatedAt); err != nil {
			return nil, err
		}
		if i, ok := indices[a.MessageID]; ok {
			ticket.Messages[i].Attachments = append(ticket.Messages[i].Attachments, a)
		}
	}
	return ticket, rows.Err()
}

// Create stores a ticket, opening message, and attachments under the user
// creation lock. The service supplies validated inputs and a digest of
// submitted content. A matching request key returns its previous ticket before
// checking the open-ticket limit.
//
// Locking the user serializes count and insertion across processes. Financial
// order ownership is checked in the same transaction, then the ticket and
// first personal message are inserted. finishTicket reads the assembled result
// and commits all records together; any earlier failure rolls back their
// writes.
func (r *ticketRepository) Create(ctx context.Context, actor support.TicketActor, input *support.CreateTicketInput, maxOpen int, requestHash string) (*support.Ticket, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// Rollback releases unfinished writes on every failure path; after a
	// successful commit, the deferred call returns the expected completed-transaction error.
	defer func() { _ = tx.Rollback() }()
	// Lock the owner before counting and inserting, serializing creation across
	// application instances so parallel requests share one open-ticket limit
	// decision.
	var userID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, actor.UserID).Scan(&userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, support.ErrTicketNotFound
		}
		return nil, err
	}
	if input.IdempotencyKey != "" {
		var id int64
		var hash string
		err := tx.QueryRowContext(ctx, `SELECT id, request_hash FROM support_tickets WHERE user_id = $1 AND request_key = $2`, actor.UserID, input.IdempotencyKey).Scan(&id, &hash)
		if err == nil {
			if hash != requestHash {
				return nil, support.ErrTicketIdempotencyConflict
			}
			return finishTicket(ctx, tx, actor, id)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM support_tickets WHERE user_id = $1 AND status IN ('pending', 'waiting_user')`, actor.UserID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= maxOpen {
		return nil, support.ErrTicketOpenLimit
	}
	if input.OrderID != nil {
		var orderID int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM payment_orders WHERE id = $1 AND user_id = $2 FOR SHARE`, *input.OrderID, actor.UserID).Scan(&orderID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, support.ErrTicketOrderInvalid
			}
			return nil, err
		}
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO support_tickets (user_id, type, title, content, priority, order_id, request_key, request_hash)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, actor.UserID, input.Type, input.Title, input.Content, input.Priority, input.OrderID, input.IdempotencyKey, requestHash).Scan(&id)
	if err != nil {
		return nil, err
	}
	// The opening message represents a personal question even for administrator
	// accounts, so it does not start the staff-reply expiration clock.
	if _, err := insertTicketMessage(ctx, tx, id, actor.UserID, false, input.Content, input.Attachments, "", ""); err != nil {
		return nil, err
	}
	return finishTicket(ctx, tx, actor, id)
}

// lockTicket acquires the accessible ticket row lock and returns its current
// state. Callers keep the transaction open through their state check and
// update, preventing concurrent replies or closure from intervening.
func lockTicket(ctx context.Context, tx *sql.Tx, actor support.TicketActor, id int64) (string, error) {
	var status string
	err := tx.QueryRowContext(ctx, `SELECT status FROM support_tickets WHERE id = $1 AND ($2 OR user_id = $3) FOR UPDATE`, id, actor.IsAdmin, actor.UserID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", support.ErrTicketNotFound
	}
	return status, err
}

// insertTicketMessage inserts one conversation message followed by its file
// records. The caller owns the transaction and commits only after all inserts
// succeed, keeping attachment bytes and their message atomic.
func insertTicketMessage(ctx context.Context, tx *sql.Tx, ticketID, userID int64, isStaff bool, content string, files []support.TicketAttachmentUpload, key, hash string) (int64, error) {
	var messageID int64
	err := tx.QueryRowContext(ctx, `INSERT INTO support_ticket_messages (ticket_id, user_id, is_staff, content, request_key, request_hash)
 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, ticketID, userID, isStaff, content, key, hash).Scan(&messageID)
	if err != nil {
		return 0, err
	}
	for _, file := range files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO support_ticket_attachments (message_id, filename, content_type, size, data) VALUES ($1,$2,$3,$4,$5)`, messageID, file.Name, file.ContentType, len(file.Data), file.Data); err != nil {
			return 0, err
		}
	}
	return messageID, nil
}

// finishTicket assembles the transaction result before committing the ticket
// operation. Read failures leave the transaction uncommitted, and callers
// defer rollback so every failure releases locks and partial writes.
func finishTicket(ctx context.Context, tx *sql.Tx, actor support.TicketActor, id int64) (*support.Ticket, error) {
	ticket, err := getTicket(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ticket, nil
}

// Reply appends one message and its attachments while holding the accessible
// ticket lock. The service supplies validated content and its request digest.
// A matching prior key returns the saved conversation even if a later action
// has closed it.
//
// A new reply requires an open state. Staff replies switch to waiting_user and
// update the expiration timestamp; personal replies switch to pending. The
// returned creation marker is set only after finishTicket commits, letting
// handlers notify for this message while idempotent retries remain silent.
func (r *ticketRepository) Reply(ctx context.Context, actor support.TicketActor, id int64, input *support.ReplyTicketInput, requestHash string) (*support.Ticket, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// Rollback releases unfinished writes on every failure path; after a
	// successful commit, the deferred call returns the expected completed-transaction error.
	defer func() { _ = tx.Rollback() }()
	status, err := lockTicket(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	if input.IdempotencyKey != "" {
		var hash string
		err := tx.QueryRowContext(ctx, `SELECT request_hash FROM support_ticket_messages WHERE ticket_id = $1 AND user_id = $2 AND request_key = $3`, id, actor.UserID, input.IdempotencyKey).Scan(&hash)
		if err == nil {
			if hash != requestHash {
				return nil, support.ErrTicketIdempotencyConflict
			}
			return finishTicket(ctx, tx, actor, id)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	if !support.IsTicketOpen(status) {
		return nil, support.ErrTicketClosed
	}
	messageID, err := insertTicketMessage(ctx, tx, id, actor.UserID, actor.IsAdmin, input.Content, input.Attachments, input.IdempotencyKey, requestHash)
	if err != nil {
		return nil, err
	}
	if actor.IsAdmin {
		_, err = tx.ExecContext(ctx, `UPDATE support_tickets SET status = 'waiting_user', last_staff_reply_at = clock_timestamp(), updated_at = clock_timestamp() WHERE id = $1`, id)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE support_tickets SET status = 'pending', updated_at = clock_timestamp() WHERE id = $1`, id)
	}
	if err != nil {
		return nil, err
	}
	ticket, err := finishTicket(ctx, tx, actor, id)
	if err == nil {
		ticket.ReplyCreated = true
		ticket.ReplyMessageID = messageID
	}
	return ticket, err
}

// Close records a terminal action while holding the accessible ticket lock.
// The caller supplies completion or withdrawal after service validation.
// Repeating the same terminal action returns its first saved result, while
// other terminal states reject further changes.
//
// The authenticated entry scope determines the closing role snapshot. Status,
// closing account, role, and timestamps change together before the result is
// read and committed. ClosureCreated is set after a successful first commit,
// so administrator notification can distinguish a new action from a replay.
func (r *ticketRepository) Close(ctx context.Context, actor support.TicketActor, id int64, target string) (*support.Ticket, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// Rollback releases unfinished writes on every failure path; after a
	// successful commit, the deferred call returns the expected completed-transaction error.
	defer func() { _ = tx.Rollback() }()
	status, err := lockTicket(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	// A same-state retry returns the original closure identity and time; different
	// terminal states reject the operation before any update is issued.
	if status == target {
		return finishTicket(ctx, tx, actor, id)
	}
	if !support.IsTicketOpen(status) {
		return nil, support.ErrTicketClosed
	}
	// The entry scope determines the saved role at closing time, keeping later
	// account-role edits from rewriting the recorded support action.
	role := "user"
	if actor.IsAdmin {
		role = "admin"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE support_tickets SET status = $2, closed_by = $3, closed_by_role = $4, closed_at = clock_timestamp(), updated_at = clock_timestamp() WHERE id = $1`, id, target, actor.UserID, role); err != nil {
		return nil, err
	}
	ticket, err := finishTicket(ctx, tx, actor, id)
	if err == nil {
		ticket.ClosureCreated = true
	}
	return ticket, err
}

// Update changes an open ticket priority under the administrator ticket lock.
// Callers supply a validated priority and an authenticated administrator
// actor. Personal scope is rejected before beginning the transaction.
//
// The row lock covers both current-state validation and the priority update,
// so a concurrent closure cannot be overwritten by a stale decision. The
// updated detail is assembled before commit, and failures roll back the
// change. Closed tickets retain their final priority and reject this
// operation.
func (r *ticketRepository) Update(ctx context.Context, actor support.TicketActor, id int64, input *support.UpdateTicketInput) (*support.Ticket, error) {
	if !actor.IsAdmin {
		return nil, support.ErrTicketNotFound
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// Rollback releases unfinished writes on every failure path; after a
	// successful commit, the deferred call returns the expected completed-transaction error.
	defer func() { _ = tx.Rollback() }()
	status, err := lockTicket(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	if !support.IsTicketOpen(status) {
		return nil, support.ErrTicketClosed
	}
	if _, err := tx.ExecContext(ctx, `UPDATE support_tickets SET priority = $2, updated_at = clock_timestamp() WHERE id = $1`, id, input.Priority); err != nil {
		return nil, err
	}
	return finishTicket(ctx, tx, actor, id)
}

// Attachment reads private file bytes after checking both attachment
// membership and ticket visibility. Personal callers can access their own
// ticket files, while administrator scope permits support downloads across
// owners.
func (r *ticketRepository) Attachment(ctx context.Context, actor support.TicketActor, ticketID, attachmentID int64) (*support.TicketAttachment, error) {
	a := &support.TicketAttachment{}
	err := r.db.QueryRowContext(ctx, `SELECT a.id, a.message_id, a.filename, a.content_type, a.size, a.created_at, a.data
 FROM support_ticket_attachments a JOIN support_ticket_messages m ON m.id = a.message_id JOIN support_tickets t ON t.id = m.ticket_id
 WHERE a.id = $1 AND t.id = $2 AND ($3 OR t.user_id = $4)`, attachmentID, ticketID, actor.IsAdmin, actor.UserID).Scan(&a.ID, &a.MessageID, &a.Filename, &a.ContentType, &a.Size, &a.CreatedAt, &a.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, support.ErrTicketNotFound
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// Expire conditionally closes tickets still waiting for a user before the
// cutoff. PostgreSQL rechecks conditions after concurrent row locks, so a user
// reply that changes waiting state prevents expiration.
func (r *ticketRepository) Expire(ctx context.Context, cutoff time.Time) (int64, error) {
	// PostgreSQL rechecks the waiting-state predicate after competing row locks
	// release, preventing a completed user reply from being overwritten by
	// expiration.
	result, err := r.db.ExecContext(ctx, `UPDATE support_tickets SET status = 'expired', closed_by = NULL, closed_by_role = 'system', closed_at = clock_timestamp(), updated_at = clock_timestamp()
 WHERE status = 'waiting_user' AND last_staff_reply_at IS NOT NULL AND last_staff_reply_at <= $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
