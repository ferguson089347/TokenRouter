package ticket

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRuntimeCommittedEvents keeps replayed and personal actions out of staff
// mail while assigning distinct identities to new replies and terminal actions.
// It verifies queue contents before workers begin delivery.
func TestRuntimeCommittedEvents(t *testing.T) {
	runtime := NewTicketRuntime(nil, nil, nil, nil)
	defer runtime.Stop()
	runtime.NotifyReply(&Ticket{ID: 1, ReplyMessageID: 7})
	runtime.NotifyClosure(&Ticket{ID: 1, ClosureCreated: true, ClosedByRole: "user", Status: TicketStatusCompleted})
	require.Empty(t, runtime.queue)
	runtime.NotifyReply(&Ticket{ID: 1, UserID: 3, ReplyCreated: true, ReplyMessageID: 7})
	reply := <-runtime.queue
	require.Equal(t, int64(7), reply.messageID)
	require.Equal(t, NotificationEmailEventTicketStaffReply, reply.event)
	runtime.NotifyClosure(&Ticket{ID: 1, UserID: 3, ClosureCreated: true, ClosedByRole: "admin", Status: TicketStatusCompleted})
	require.Equal(t, NotificationEmailEventTicketCompleted, (<-runtime.queue).event)
}

// TestRuntimeCapacityAndShutdown fills the queue before startup and stops an
// active worker pair. It verifies bounded admission, cancellation, and rejection
// of events queued after the runtime has stopped.
func TestRuntimeCapacityAndShutdown(t *testing.T) {
	config := NewTicketConfigService(&ticketConfigRepoStub{})
	runtime := NewTicketRuntime(nil, config, nil, nil)
	for i := range cap(runtime.queue) + 1 {
		runtime.NotifyReply(&Ticket{ID: 1, ReplyCreated: true, ReplyMessageID: int64(i + 1)})
	}
	require.Len(t, runtime.queue, cap(runtime.queue))
	runtime.Start()
	runtime.Start()
	runtime.Stop()
	require.ErrorIs(t, runtime.ctx.Err(), context.Canceled)
	for len(runtime.queue) > 0 {
		<-runtime.queue
	}
	runtime.NotifyReply(&Ticket{ID: 1, ReplyCreated: true, ReplyMessageID: 999})
	require.Empty(t, runtime.queue)
	runtime.Stop()
}
