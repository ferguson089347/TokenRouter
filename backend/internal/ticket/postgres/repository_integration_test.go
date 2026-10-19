//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
	support "github.com/TokenFlux/TokenRouter/internal/ticket"
	"github.com/TokenFlux/TokenRouter/migrations"
)

// ticketIntegrationActors creates ordinary and staff accounts in a fresh
// migrated PostgreSQL fixture. Cleanup closes the fixture after the test, and
// callers receive its SQL pool for independent transaction assertions.
func ticketIntegrationActors(t *testing.T) (*sql.DB, support.TicketActor, support.TicketActor) {
	t.Helper()
	// Use a dedicated PostgreSQL fixture with cleanup registered before commits,
	// keeping concurrency and migration assertions isolated from the application
	// database.
	integrationDB := postgrescontainer.New(t)
	var userID, staffID int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `INSERT INTO users (email,password_hash) VALUES ('ticket-user@example.com','test') RETURNING id`).Scan(&userID))
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `INSERT INTO users (email,password_hash,role) VALUES ('ticket-staff@example.com','test','admin') RETURNING id`).Scan(&staffID))
	return integrationDB, support.TicketActor{UserID: userID}, support.TicketActor{UserID: staffID, IsAdmin: true}
}

// TestTicketIntegrationMigrationAndConcurrentCreateLimit exercises parallel
// ticket creation using independent SQL transactions. The owner row lock admits
// only the configured number of open tickets, and database attachment checks
// remain active.
func TestTicketIntegrationMigrationAndConcurrentCreateLimit(t *testing.T) {
	ctx := context.Background()
	integrationDB, actor, _ := ticketIntegrationActors(t)
	// Replay the table migration against existing records and verify the database
	// still enforces attachment byte counts independently of service validation.
	contents, err := migrations.FS.ReadFile("300_support_tickets.sql")
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, string(contents))
	require.NoError(t, err)
	repo := NewTicketRepository(integrationDB)
	const requests = 8
	results := make([]error, requests)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range requests {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, results[i] = repo.Create(ctx, actor, &support.CreateTicketInput{Type: "technical", Title: fmt.Sprintf("工单%d", i), Content: "详情", Priority: "normal"}, 2, "")
		}(i)
	}
	close(start)
	wg.Wait()
	success := 0
	for _, err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, support.ErrTicketOpenLimit)
		}
	}
	require.Equal(t, 2, success)
	list, err := repo.List(ctx, actor, support.TicketListFilter{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 2, list.Total)
	var messageID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT id FROM support_ticket_messages LIMIT 1`).Scan(&messageID))
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO support_ticket_attachments (message_id,filename,content_type,size,data) VALUES ($1,'x.pdf','application/pdf',2,$2)`, messageID, []byte{1})
	require.Error(t, err)
}

// TestTicketIntegrationLifecycleAttachmentAndIdempotency exercises an owned
// ticket through replies, attachments, priorities, and closing. PostgreSQL
// retains one result for each request key and rejects access by unrelated
// personal actors.
func TestTicketIntegrationLifecycleAttachmentAndIdempotency(t *testing.T) {
	ctx := context.Background()
	integrationDB, actor, staff := ticketIntegrationActors(t)
	repo := NewTicketRepository(integrationDB)
	created, err := repo.Create(ctx, actor, &support.CreateTicketInput{Type: "technical", Title: "标题", Content: "详情", Priority: "high", IdempotencyKey: "create-key", Attachments: []support.TicketAttachmentUpload{{Name: "a.pdf", ContentType: "application/pdf", Data: []byte("%PDF-test")}}}, 1, "hash")
	require.NoError(t, err)
	repeated, err := repo.Create(ctx, actor, &support.CreateTicketInput{IdempotencyKey: "create-key"}, 1, "hash")
	require.NoError(t, err)
	require.Equal(t, created.ID, repeated.ID)
	_, err = repo.Create(ctx, actor, &support.CreateTicketInput{IdempotencyKey: "create-key"}, 1, "other")
	require.ErrorIs(t, err, support.ErrTicketIdempotencyConflict)
	require.Len(t, created.Messages, 1)
	require.Len(t, created.Messages[0].Attachments, 1)
	fileID := created.Messages[0].Attachments[0].ID
	_, err = repo.Get(ctx, support.TicketActor{UserID: staff.UserID}, created.ID)
	require.ErrorIs(t, err, support.ErrTicketNotFound)
	_, err = repo.Attachment(ctx, support.TicketActor{UserID: staff.UserID}, created.ID, fileID)
	require.ErrorIs(t, err, support.ErrTicketNotFound)
	file, err := repo.Attachment(ctx, staff, created.ID, fileID)
	require.NoError(t, err)
	require.Equal(t, []byte("%PDF-test"), file.Data)
	replied, err := repo.Reply(ctx, staff, created.ID, &support.ReplyTicketInput{Content: "请补充", IdempotencyKey: "reply-key"}, "reply-hash")
	require.NoError(t, err)
	require.Equal(t, "waiting_user", replied.Status)
	// Staff replies retain the actual sender while the compatibility assignment
	// column remains unset, preventing a reply from implicitly assigning a
	// handler.
	require.Equal(t, staff.UserID, replied.Messages[1].UserID)
	require.True(t, replied.Messages[1].IsStaff)
	var assigned sql.NullInt64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT assigned_to FROM support_tickets WHERE id=$1`, created.ID).Scan(&assigned))
	require.False(t, assigned.Valid)
	require.NotNil(t, replied.LastStaffReplyAt)
	require.True(t, replied.ReplyCreated)
	require.NotZero(t, replied.ReplyMessageID)
	_, err = repo.Close(ctx, actor, created.ID, "completed")
	require.NoError(t, err)
	repeated, err = repo.Reply(ctx, staff, created.ID, &support.ReplyTicketInput{Content: "请补充", IdempotencyKey: "reply-key"}, "reply-hash")
	require.NoError(t, err)
	require.False(t, repeated.ReplyCreated)
	require.Len(t, repeated.Messages, 2)
	_, err = repo.Reply(ctx, actor, created.ID, &support.ReplyTicketInput{Content: "迟到回复"}, "")
	require.ErrorIs(t, err, support.ErrTicketClosed)
	_, err = repo.Close(ctx, actor, created.ID, "completed")
	require.NoError(t, err)
	_, err = repo.Close(ctx, actor, created.ID, "cancelled")
	require.ErrorIs(t, err, support.ErrTicketClosed)
}

// TestTicketIntegrationExpirationCannotOverwriteConcurrentUserReply exercises an
// expiration update waiting on a concurrent personal reply transaction.
// PostgreSQL rechecks waiting state after the row lock releases and leaves the
// replied ticket open.
func TestTicketIntegrationExpirationCannotOverwriteConcurrentUserReply(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	integrationDB, actor, staff := ticketIntegrationActors(t)
	repo := NewTicketRepository(integrationDB)
	created, err := repo.Create(ctx, actor, &support.CreateTicketInput{Type: "other", Title: "标题", Content: "详情", Priority: "normal"}, 3, "")
	require.NoError(t, err)
	_, err = repo.Reply(ctx, staff, created.ID, &support.ReplyTicketInput{Content: "请补充"}, "")
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE support_tickets SET last_staff_reply_at = NOW() - INTERVAL '2 days' WHERE id = $1`, created.ID)
	require.NoError(t, err)
	// Hold the same ticket lock as a user reply before launching expiration, so
	// its conditional update waits for and rechecks the committed pending state.
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	var id int64
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT id FROM support_tickets WHERE id = $1 FOR UPDATE`, created.ID).Scan(&id))
	result := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		count, err := repo.Expire(ctx, time.Now().Add(-24*time.Hour))
		if err == nil && count != 0 {
			err = errors.New("expiration overwrote user reply")
		}
		result <- err
	}()
	<-started
	_, err = tx.ExecContext(ctx, `UPDATE support_tickets SET status = 'pending' WHERE id = $1`, created.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	require.NoError(t, <-result)
	after, err := repo.Get(ctx, actor, created.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", after.Status)
	_, err = repo.Reply(ctx, staff, created.ID, &support.ReplyTicketInput{Content: "新的工作人员回复"}, "")
	require.NoError(t, err)
	count, err := repo.Expire(ctx, time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	require.Zero(t, count, "最新工作人员回复应重新开始计时")
}

// TestTicketMigrationPreservesForkConversation rebuilds the schema shape used by
// the fork with a historical category and priority. It verifies forward migrations
// retain identifiers, message text, attachment bytes, retry hashes, and unknown closing identities.
func TestTicketMigrationPreservesForkConversation(t *testing.T) {
	db, actor, _ := ticketIntegrationActors(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
 DROP TRIGGER support_ticket_normalize_type ON support_tickets;
 DROP TRIGGER support_ticket_normalize_priority ON support_tickets;
 ALTER TABLE support_tickets DROP CONSTRAINT support_tickets_type_check;
 ALTER TABLE support_tickets DROP CONSTRAINT support_tickets_priority_check;
 ALTER TABLE support_tickets DROP COLUMN closed_by, DROP COLUMN closed_by_role;`)
	require.NoError(t, err)
	var ticketID, messageID, fileID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO support_tickets(user_id,type,title,content,priority,status,request_key,request_hash) VALUES ($1,'presales','历史标题','历史问题','urgent','completed','fork-request','fork-digest') RETURNING id`, actor.UserID).Scan(&ticketID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO support_ticket_messages(ticket_id,user_id,content,request_key,request_hash) VALUES ($1,$2,'历史回复','reply-request','reply-digest') RETURNING id`, ticketID, actor.UserID).Scan(&messageID))
	original := []byte("private fork attachment bytes")
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO support_ticket_attachments(message_id,filename,content_type,size,data) VALUES ($1,'历史附件.pdf','application/pdf',$2,$3) RETURNING id`, messageID, len(original), original).Scan(&fileID))
	// Repeat the forward sequence to cover both fork imports and already migrated databases.
	// Neither pass may recreate conversation identifiers or replace private file bytes.
	for range 2 {
		for _, name := range []string{"300_support_tickets.sql", "301_support_ticket_consultation_type.sql", "302_support_ticket_three_priorities.sql", "303_support_ticket_closure_actor.sql"} {
			data, readErr := migrations.FS.ReadFile(name)
			require.NoError(t, readErr)
			_, applyErr := db.ExecContext(ctx, string(data))
			require.NoError(t, applyErr)
		}
	}
	repo := NewTicketRepository(db)
	result, err := repo.Get(ctx, actor, ticketID)
	require.NoError(t, err)
	require.Equal(t, "consultation", result.Type)
	require.Equal(t, "high", result.Priority)
	require.Nil(t, result.ClosedBy)
	require.Empty(t, result.ClosedByRole)
	require.Equal(t, "历史回复", result.Messages[0].Content)
	require.Equal(t, messageID, result.Messages[0].ID)
	require.Equal(t, fileID, result.Messages[0].Attachments[0].ID)
	attachment, err := repo.Attachment(ctx, actor, ticketID, fileID)
	require.NoError(t, err)
	require.Equal(t, original, attachment.Data)
	var digest string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT request_hash FROM support_tickets WHERE id=$1`, ticketID).Scan(&digest))
	require.Equal(t, "fork-digest", digest)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT request_hash FROM support_ticket_messages WHERE id=$1`, messageID).Scan(&digest))
	require.Equal(t, "reply-digest", digest)
}
