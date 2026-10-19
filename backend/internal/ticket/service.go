package ticket

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// service.go validates support-ticket requests and keeps request identities
// stable across input normalization. Personal callers operate on their own
// tickets, while the administrator entry supplies a trusted staff identity.
// Every business method reads current configuration before accessing stored
// tickets, so disabling the module affects already open browser sessions as
// well as navigation.
//
// Creation and replies validate attachments before persistence. Legacy
// category and priority inputs are normalized for storage, while their
// submitted values remain in the request digest. This lets clients retry
// earlier requests using the same key after an upgrade. The repository applies
// ownership checks and state changes within the transaction that writes
// messages and files.

const (
	// TicketMaxTitleLength bounds new subjects in Unicode characters; historical
	// stored titles are returned at their existing length.
	TicketMaxTitleLength = 50

	// Ticket states distinguish the next reply actor from final conversations.
	// Repositories and user interfaces use these values to permit supported actions.
	TicketStatusPending     = "pending"
	TicketStatusWaitingUser = "waiting_user"
	TicketStatusCompleted   = "completed"
	TicketStatusCancelled   = "cancelled"
	TicketStatusExpired     = "expired"
)

// Ticket errors retain stable public reasons for ownership, lifecycle, limits,
// and request conflicts so HTTP clients can choose localized guidance.
var (
	ErrTicketNotFound            = apperror.NotFound("TICKET_NOT_FOUND", "工单不存在")
	ErrTicketClosed              = apperror.Conflict("TICKET_CLOSED", "该工单已结束，无法继续处理")
	ErrTicketOpenLimit           = apperror.Conflict("TICKET_OPEN_LIMIT", "您还有工单未处理，请先完成或撤销已有工单")
	ErrTicketOrderInvalid        = apperror.BadRequest("TICKET_ORDER_INVALID", "请选择当前用户的一笔有效订单")
	ErrTicketInputInvalid        = apperror.BadRequest("TICKET_INPUT_INVALID", "工单输入无效")
	ErrTicketTitleTooLong        = apperror.BadRequest("TICKET_TITLE_TOO_LONG", "工单标题最多 50 个字符")
	ErrTicketDisabled            = apperror.Forbidden("TICKET_DISABLED", "工单功能暂未开放")
	ErrTicketIdempotencyConflict = apperror.Conflict("TICKET_IDEMPOTENCY_CONFLICT", "此请求标识已经用于其他工单内容")
)

// TicketActor carries the authenticated identity and the selected support
// access scope.
type TicketActor struct {
	UserID  int64 // authenticated owner identifier
	IsAdmin bool  // administrator entry scope
}

// TicketAttachmentUpload carries file bytes and display metadata through validation.
// Validators derive trusted MIME values and replace image buffers before persistence.
type TicketAttachmentUpload struct {
	Name        string `json:"name"`         // submitted or normalized filename
	ContentType string `json:"content_type"` // submitted or detected MIME type
	Data        []byte `json:"-"`            // private file bytes
}

// TicketAttachment exposes file metadata and separately holds authenticated
// download bytes.
type TicketAttachment struct {
	ID          int64     `json:"id"`           // stored record identifier
	MessageID   int64     `json:"message_id"`   // owning message identifier
	Filename    string    `json:"filename"`     // stored display filename
	ContentType string    `json:"content_type"` // validated MIME type
	Size        int64     `json:"size"`         // stored byte count
	CreatedAt   time.Time `json:"created_at"`   // record creation timestamp
	Data        []byte    `json:"-"`            // private file bytes
}

// TicketMessage stores one conversation entry and its ordered private
// attachment metadata.
type TicketMessage struct {
	ID          int64              `json:"id"`          // stored record identifier
	TicketID    int64              `json:"ticket_id"`   // owning ticket identifier
	UserID      int64              `json:"user_id"`     // actual sender account identifier
	SenderName  string             `json:"sender_name"` // message sender display name
	IsStaff     bool               `json:"is_staff"`    // support entry identity
	Content     string             `json:"content"`     // submitted message text
	CreatedAt   time.Time          `json:"created_at"`  // record creation timestamp
	Attachments []TicketAttachment `json:"attachments"` // ordered message file records
}

// TicketOrder contains the payment summary permitted in an owned ticket
// response.
type TicketOrder struct {
	ID         int64   `json:"id"`           // stored record identifier
	OutTradeNo string  `json:"out_trade_no"` // public payment order number
	Amount     float64 `json:"amount"`       // order credit amount
	PayAmount  float64 `json:"pay_amount"`   // order paid amount
	Currency   string  `json:"currency"`     // payment currency code
	Status     string  `json:"status"`       // payment order state
}

// Ticket represents an owned conversation, lifecycle timestamps, and committed
// notification markers.
type Ticket struct {
	ID               int64        `json:"id"`                  // stored record identifier
	UserID           int64        `json:"user_id"`             // authenticated owner identifier
	UserEmail        string       `json:"user_email"`          // ticket owner email
	UserName         string       `json:"user_name"`           // ticket owner display name
	Type             string       `json:"type"`                // canonical ticket category
	Title            string       `json:"title"`               // ticket subject text
	Content          string       `json:"content"`             // submitted message text
	Priority         string       `json:"priority"`            // canonical handling priority
	Status           string       `json:"status"`              // ticket lifecycle state
	OrderID          *int64       `json:"order_id"`            // optional payment reference
	Order            *TicketOrder `json:"order"`               // restricted payment summary
	CreatedAt        time.Time    `json:"created_at"`          // record creation timestamp
	UpdatedAt        time.Time    `json:"updated_at"`          // latest activity timestamp
	LastStaffReplyAt *time.Time   `json:"last_staff_reply_at"` // latest support reply timestamp
	ClosedAt         *time.Time   `json:"closed_at"`           // first terminal action timestamp
	// ClosedBy and ClosedByRole record the authenticated closure identity and role
	// snapshot together. Historical closures with unknown actors keep nil
	// identifiers and empty roles.
	ClosedBy       *int64          `json:"closed_by"`          // optional closing account identifier
	ClosedByRole   string          `json:"closed_by_role"`     // closing actor role snapshot
	Messages       []TicketMessage `json:"messages,omitempty"` // ordered conversation message records
	ReplyCreated   bool            `json:"-"`                  // new committed reply marker
	ReplyMessageID int64           `json:"-"`                  // new committed message identifier
	// ClosureCreated is true only after the first closing transaction commits.
	// Handlers use this transient marker to suppress email on terminal retries.
	ClosureCreated bool `json:"-"` // new committed closure marker
}

// CreateTicketInput carries submitted ticket fields and an optional idempotent
// request identity.
type CreateTicketInput struct {
	Type           string                   `json:"type"`     // canonical ticket category
	Title          string                   `json:"title"`    // ticket subject text
	Content        string                   `json:"content"`  // submitted message text
	Priority       string                   `json:"priority"` // canonical handling priority
	OrderID        *int64                   `json:"order_id"` // optional payment reference
	Attachments    []TicketAttachmentUpload `json:"-"`        // ordered message file records
	IdempotencyKey string                   `json:"-"`        // optional client retry identifier
}

// ReplyTicketInput carries message text and attachments for a retryable
// conversation append.
type ReplyTicketInput struct {
	Content        string                   `json:"content"` // submitted message text
	Attachments    []TicketAttachmentUpload `json:"-"`       // ordered message file records
	IdempotencyKey string                   `json:"-"`       // optional client retry identifier
}

// UpdateTicketInput contains the administrator priority change with omission
// represented by nil.
type UpdateTicketInput struct {
	Priority *string `json:"priority"` // canonical handling priority
}

// TicketListFilter selects a page by lifecycle, category, priority, and search
// text.
type TicketListFilter struct {
	Page     int    // requested page number
	PageSize int    // requested rows per page
	Status   string // ticket lifecycle state
	Type     string // canonical ticket category
	Priority string // canonical handling priority
	Search   string // literal subject search text
}

// TicketListResult returns visible tickets with pagination counts for the same
// filters.
type TicketListResult struct {
	Items    []Ticket `json:"items"`     // visible ticket page records
	Total    int64    `json:"total"`     // matching visible ticket count
	Page     int      `json:"page"`      // requested page number
	PageSize int      `json:"page_size"` // requested rows per page
	Pages    int      `json:"pages"`     // matching page count
}

// TicketConfigProvider supplies current module availability and ticket
// submission limits.
type TicketConfigProvider interface {
	// Get returns current support settings or a configuration read failure.
	// Business callers reject a failed or nil result before deciding whether
	// module access, uploads, or expiration are permitted.
	Get(context.Context) (*TicketConfig, error)
}

// TicketRepository combines ownership validation with transactional lifecycle
// and conversation persistence.
type TicketRepository interface {
	// List applies actor ownership and returns a filtered page. Callers supply
	// validated filters and positive pagination; administrators sort by priority
	// before recent activity, while personal pages sort by recent activity.
	List(context.Context, TicketActor, TicketListFilter) (*TicketListResult, error)
	// Get returns one visible conversation and its file metadata from a consistent
	// snapshot. Missing or inaccessible personal tickets return ErrTicketNotFound,
	// and private file bytes remain behind the separate attachment operation.
	Get(context.Context, TicketActor, int64) (*Ticket, error)
	// Create locks the owner and atomically writes the ticket, opening message,
	// and files. Matching request keys reuse the saved result before checking
	// open-ticket capacity; mismatched request digests return conflict.
	Create(context.Context, TicketActor, *CreateTicketInput, int, string) (*Ticket, error)
	// Reply locks a visible ticket and commits its message, files, and waiting
	// state together. Idempotent replays return the saved conversation, while new
	// replies require an open state and mark newly committed messages.
	Reply(context.Context, TicketActor, int64, *ReplyTicketInput, string) (*Ticket, error)
	// Close locks a visible ticket and records its first terminal action and
	// authenticated role. Same-state retries retain the original closing identity,
	// while different terminal states reject further transitions.
	Close(context.Context, TicketActor, int64, string) (*Ticket, error)
	// Update changes a validated priority while holding an administrator ticket
	// lock. Personal actors and closed tickets are rejected, and the detail result
	// describes the same committed priority update.
	Update(context.Context, TicketActor, int64, *UpdateTicketInput) (*Ticket, error)
	// Attachment returns bytes only when the file belongs to a visible ticket.
	// Personal scope checks ownership even for administrator accounts, and
	// inaccessible membership returns the same not-found error.
	Attachment(context.Context, TicketActor, int64, int64) (*TicketAttachment, error)
	// Expire conditionally closes tickets waiting for a user before the supplied
	// cutoff. Concurrent replies are rechecked after row-lock waits, preventing
	// expiration from replacing a newly pending support conversation.
	Expire(context.Context, time.Time) (int64, error)
}

// TicketService validates user and administrator ticket operations against
// current stored limits.
// @project-doc docs/domains/support_tickets.md#ticket_lifecycle
type TicketService struct {
	repo   TicketRepository     // transactional ticket persistence
	config TicketConfigProvider // current ticket configuration reader
}

// NewTicketService combines transactional ticket persistence with current
// configuration validation.
func NewTicketService(repo TicketRepository, config TicketConfigProvider) *TicketService {
	return &TicketService{repo: repo, config: config}
}

// Config reads ticket limits even when the module is disabled. Missing
// providers, nil results, and storage failures return errors so callers cannot
// accidentally bypass configured restrictions.
func (s *TicketService) Config(ctx context.Context) (*TicketConfig, error) {
	if s.config == nil {
		return nil, fmt.Errorf("工单配置服务不可用")
	}
	cfg, err := s.config.Get(ctx)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, fmt.Errorf("工单配置为空")
	}
	return cfg, nil
}

// enabledConfig reads current limits and rejects disabled ticket operations.
// Configuration errors remain errors, preventing historical reads or new
// writes from proceeding with unknown module availability.
func (s *TicketService) enabledConfig(ctx context.Context) (*TicketConfig, error) {
	cfg, err := s.Config(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, ErrTicketDisabled
	}
	return cfg, nil
}

// List validates filters and bounds pagination before querying visible
// tickets. Missing page sizes select twenty rows, sizes above one hundred are
// clamped, and invalid filter values return an input error.
func (s *TicketService) List(ctx context.Context, actor TicketActor, filter TicketListFilter) (*TicketListResult, error) {
	if actor.UserID <= 0 {
		return nil, ErrTicketNotFound
	}
	if _, err := s.enabledConfig(ctx); err != nil {
		return nil, err
	}
	filter.Type = normalizeTicketType(filter.Type)
	filter.Priority = normalizeTicketPriority(filter.Priority)
	if filter.Status != "" && !validTicketStatus(filter.Status) || filter.Type != "" && !validTicketType(filter.Type) || filter.Priority != "" && !validTicketPriority(filter.Priority) {
		return nil, ErrTicketInputInvalid
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 20
	}
	if filter.PageSize > 100 {
		filter.PageSize = 100
	}
	filter.Search = strings.TrimSpace(filter.Search)
	if utf8.RuneCountInString(filter.Search) > 200 || filter.Page > 1000000 {
		return nil, ErrTicketInputInvalid
	}
	return s.repo.List(ctx, actor, filter)
}

// Get returns the requested ticket through the repository ownership check.
// Invalid identities or identifiers return not found, and current module
// availability is checked before reading any stored conversation.
func (s *TicketService) Get(ctx context.Context, actor TicketActor, id int64) (*Ticket, error) {
	if actor.UserID <= 0 || id <= 0 {
		return nil, ErrTicketNotFound
	}
	if _, err := s.enabledConfig(ctx); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, actor, id)
}

// Create validates a personal ticket and persists its opening message together
// with attachments. The actor comes from authentication; order references are
// permitted for financial tickets and ownership is checked by the repository.
//
// The submitted category and priority contribute to the request digest before
// their normalized values are persisted. This maintains idempotent retries
// from clients using historical aliases. Titles use Unicode character counts,
// bodies are trimmed, and attachment validation completes before the
// repository locks the user and checks the open-ticket limit.
func (s *TicketService) Create(ctx context.Context, actor TicketActor, input *CreateTicketInput) (*Ticket, error) {
	if actor.UserID <= 0 {
		return nil, ErrTicketNotFound
	}
	cfg, err := s.enabledConfig(ctx)
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, ErrTicketInputInvalid
	}
	in := *input
	// The digest retains the submitted category so retries from historical forms
	// still match their persisted request identity after normalization.
	requestedType := in.Type
	in.Type = normalizeTicketType(in.Type)
	in.Title = strings.TrimSpace(in.Title)
	in.Content = strings.TrimSpace(in.Content)
	if in.Priority == "" {
		in.Priority = "normal"
	}
	// The submitted priority remains in the digest; normalizing urgent for storage
	// must not redirect an existing key to a different request.
	requestedPriority := in.Priority
	in.Priority = normalizeTicketPriority(in.Priority)
	if !validTicketType(in.Type) || !validTicketPriority(in.Priority) || in.Title == "" || in.Content == "" || utf8.RuneCountInString(in.Content) > 20000 || !validTicketRequestKey(in.IdempotencyKey) {
		return nil, ErrTicketInputInvalid
	}
	// Unicode code points match the browser counter and accept multibyte subjects
	// within the same visible character allowance.
	if utf8.RuneCountInString(in.Title) > TicketMaxTitleLength {
		return nil, ErrTicketTitleTooLong
	}
	if in.OrderID != nil && (in.Type != "financial" || *in.OrderID <= 0) {
		return nil, ErrTicketOrderInvalid
	}
	in.Attachments, err = ValidateTicketAttachments(in.Attachments, cfg)
	if err != nil {
		return nil, err
	}
	hashInput := in
	hashInput.Type = requestedType
	hashInput.Priority = requestedPriority
	return s.repo.Create(ctx, actor, &in, cfg.MaxOpenTickets, ticketRequestHash(hashInput, in.Attachments))
}

// Reply validates text and files before appending to an accessible open
// ticket. The repository locks the ticket, recognizes idempotent replays, and
// changes the waiting status according to the authenticated actor.
func (s *TicketService) Reply(ctx context.Context, actor TicketActor, id int64, input *ReplyTicketInput) (*Ticket, error) {
	if actor.UserID <= 0 || id <= 0 {
		return nil, ErrTicketNotFound
	}
	cfg, err := s.enabledConfig(ctx)
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, ErrTicketInputInvalid
	}
	in := *input
	in.Content = strings.TrimSpace(in.Content)
	if (in.Content == "" && len(in.Attachments) == 0) || utf8.RuneCountInString(in.Content) > 20000 || !validTicketRequestKey(in.IdempotencyKey) {
		return nil, ErrTicketInputInvalid
	}
	in.Attachments, err = ValidateTicketAttachments(in.Attachments, cfg)
	if err != nil {
		return nil, err
	}
	return s.repo.Reply(ctx, actor, id, &in, ticketRequestHash(in, in.Attachments))
}

// Close accepts completion or withdrawal for an authenticated ticket actor.
// The repository records the first closing identity and rejects transitions
// between different terminal states, including a previously expired
// conversation.
func (s *TicketService) Close(ctx context.Context, actor TicketActor, id int64, status string) (*Ticket, error) {
	if actor.UserID <= 0 || id <= 0 {
		return nil, ErrTicketNotFound
	}
	if _, err := s.enabledConfig(ctx); err != nil {
		return nil, err
	}
	if status != TicketStatusCompleted && status != TicketStatusCancelled {
		return nil, ErrTicketInputInvalid
	}
	return s.repo.Close(ctx, actor, id, status)
}

// Update permits authenticated administrators to change an open ticket
// priority. Historical urgent values normalize to high, while omitted
// priorities and unsupported values return an input error before persistence.
func (s *TicketService) Update(ctx context.Context, actor TicketActor, id int64, in *UpdateTicketInput) (*Ticket, error) {
	if !actor.IsAdmin || actor.UserID <= 0 || id <= 0 {
		return nil, ErrTicketNotFound
	}
	if _, err := s.enabledConfig(ctx); err != nil {
		return nil, err
	}
	if in == nil || in.Priority == nil {
		return nil, ErrTicketInputInvalid
	}
	priority := normalizeTicketPriority(*in.Priority)
	if !validTicketPriority(priority) {
		return nil, ErrTicketInputInvalid
	}
	return s.repo.Update(ctx, actor, id, &UpdateTicketInput{Priority: &priority})
}

// Attachment returns private file content after module and identity checks.
// The repository checks both ticket ownership and attachment membership,
// giving personal callers the same visibility as conversation reads.
func (s *TicketService) Attachment(ctx context.Context, actor TicketActor, ticketID, attachmentID int64) (*TicketAttachment, error) {
	if actor.UserID <= 0 || ticketID <= 0 || attachmentID <= 0 {
		return nil, ErrTicketNotFound
	}
	if _, err := s.enabledConfig(ctx); err != nil {
		return nil, err
	}
	return s.repo.Attachment(ctx, actor, ticketID, attachmentID)
}

// Expire closes tickets still awaiting a user beyond the configured staff-
// reply age. Disabled modules and a zero-hour timeout skip scanning, while
// configuration failures propagate to the runtime logger.
func (s *TicketService) Expire(ctx context.Context) (int64, error) {
	cfg, err := s.Config(ctx)
	if err != nil {
		return 0, err
	}
	if !cfg.Enabled || cfg.AutoExpireHours <= 0 {
		return 0, nil
	}
	return s.repo.Expire(ctx, time.Now().Add(-time.Duration(cfg.AutoExpireHours)*time.Hour))
}

// validTicketType reports whether a value names a supported canonical ticket
// category.
func validTicketType(v string) bool {
	switch v {
	case "consultation", "financial", "technical":
		return true
	}
	return false
}

// normalizeTicketType maps historical sales and miscellaneous categories to
// consultation. Unknown inputs pass through for validation, allowing callers
// to reject unsupported values while retaining the submitted value for request
// hashing.
func normalizeTicketType(v string) string {
	switch v {
	case "presales", "aftersales", "other":
		return "consultation"
	default:
		return v
	}
}

// validTicketPriority reports whether a value names low, medium, or high
// priority.
func validTicketPriority(v string) bool {
	switch v {
	case "low", "normal", "high":
		return true
	}
	return false
}

// normalizeTicketPriority maps the historical urgent priority to canonical
// high. Other inputs pass through unchanged for validation, and creation
// retains the submitted priority separately for idempotent replay checks.
func normalizeTicketPriority(v string) string {
	if v == "urgent" {
		return "high"
	}
	return v
}

// validTicketStatus reports whether a value names an accepted ticket lifecycle
// state.
func validTicketStatus(v string) bool {
	switch v {
	case TicketStatusPending, TicketStatusWaitingUser, TicketStatusCompleted, TicketStatusCancelled, TicketStatusExpired:
		return true
	}
	return false
}

// IsTicketOpen identifies the two states that accept further conversation
// messages.
func IsTicketOpen(status string) bool {
	return status == TicketStatusPending || status == TicketStatusWaitingUser
}

// validTicketRequestKey accepts an empty key or printable ASCII up to 128
// bytes. Control characters and whitespace are rejected so request keys remain
// suitable for persisted retry identification.
func validTicketRequestKey(key string) bool {
	if len(key) > 128 {
		return false
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

// ticketRequestHash hashes serialized request fields and ordered attachment
// metadata and bytes. Callers supply validated request structs whose JSON
// fields represent the submitted operation. Attachment data is excluded from
// JSON and included through a separate SHA-256 digest for each file.
//
// File ordering, normalized names, and trusted content types participate in
// the identity. Reusing a key with different message text or files therefore
// conflicts in persistence. The result is a lowercase hexadecimal SHA-256
// string suitable for the fixed database column.
func ticketRequestHash(input any, files []TicketAttachmentUpload) string {
	h := sha256.New()
	data, _ := json.Marshal(input)
	_, _ = h.Write(data)
	for _, file := range files {
		meta, _ := json.Marshal([]string{file.Name, file.ContentType})
		_, _ = h.Write(meta)
		sum := sha256.Sum256(file.Data)
		_, _ = h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}
