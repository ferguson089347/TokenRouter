package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// channel_writer.go buffers errors and protocol preambles before automatic channel
// replay. Successful nonstreaming output commits immediately, and streaming output
// commits at its first business event. Framing retains incomplete SSE tails across
// writes, including CRLF delimiters, so a split error cannot be mistaken for
// successful generated content. Committed streams continue straight to the client.
//
// Pending data is bounded at 256 KiB. Reaching that bound commits the response and
// closes replay even when framing remains incomplete. Header and status changes
// stay local until commit; the coordinator discards a failed attempt's buffered
// bytes when another channel starts, or returns the last actual error after
// candidates are exhausted. Write failures remain observable through final commit.

// channelRoutingPendingLimit bounds preamble and error retention before committing oversized responses to the client.
const channelRoutingPendingLimit = 256 * 1024

// channelRoutingAttemptWriter retains one attempt's headers, failure evidence and bounded pending output.
type channelRoutingAttemptWriter struct {
	gin.ResponseWriter              // Underlying final client output.
	ctx                *gin.Context // Current upstream diagnostic context.
	header             http.Header  // Attempt-local mutable response headers.
	status             int          // Pending HTTP response status.
	size               int          // Accepted bytes; minus one means untouched.
	committed          bool         // Business output has reached the parent.
	failure            bool         // Explicit protocol failure was observed.
	pending            bytes.Buffer // Bounded uncommitted response bytes.
	ssePartial         []byte       // Incomplete event frame retained across writes.
	sseDiscard         bool         // Oversized event is being skipped through its delimiter.
	writeErr           error        // First downstream delivery failure.
}

// newChannelRoutingAttemptWriter prepares isolated output for one automatic attempt.
// The coordinator retains ownership of parent and commits this writer only when
// output closes replay or channel selection finishes.
func newChannelRoutingAttemptWriter(parent gin.ResponseWriter) *channelRoutingAttemptWriter {
	return &channelRoutingAttemptWriter{ResponseWriter: parent, header: parent.Header().Clone(), status: http.StatusOK, size: -1}
}

// Header returns the mutable headers retained by this routing attempt.
func (w *channelRoutingAttemptWriter) Header() http.Header { return w.header }

// Status returns the selected HTTP status before or after commit.
func (w *channelRoutingAttemptWriter) Status() int { return w.status }

// Size returns accepted bytes, with minus one marking untouched output.
func (w *channelRoutingAttemptWriter) Size() int { return w.size }

// Written reports whether this attempt has accepted headers or body bytes.
func (w *channelRoutingAttemptWriter) Written() bool { return w.size >= 0 }

// WriteHeader selects the attempt status while its output remains untouched.
func (w *channelRoutingAttemptWriter) WriteHeader(code int) {
	if !w.Written() && code > 0 {
		w.status = code
	}
}

// WriteHeaderNow records local commitment while buffering the underlying parent response.
func (w *channelRoutingAttemptWriter) WriteHeaderNow() {
	if w.size < 0 {
		w.size = 0
	}
}

// WriteString sends text through bounded error and SSE framing handling.
func (w *channelRoutingAttemptWriter) WriteString(body string) (int, error) {
	return w.Write([]byte(body))
}

// Write retains bounded failure output and commits the first successful business event.
// A response exceeding the pending limit commits before copying the oversized data.
// After commitment every byte goes directly to the parent and replay stays closed.
func (w *channelRoutingAttemptWriter) Write(body []byte) (int, error) {
	w.WriteHeaderNow()
	w.size += len(body)
	isSSE := strings.Contains(w.header.Get("Content-Type"), "text/event-stream")
	business := false
	if isSSE {
		business = w.observeSSE(body)
	}
	if w.committed {
		return w.writeCommitted(body)
	}
	// An oversized pending response closes replay before copying its remaining
	// bytes so memory use stays bounded even for a single very large write.
	if len(body) > channelRoutingPendingLimit-w.pending.Len() {
		if err := w.commit(); err != nil {
			return 0, err
		}
		return w.writeCommitted(body)
	}
	_, _ = w.pending.Write(body)
	if w.status >= 400 {
		return len(body), nil
	}
	if !isSSE {
		errorValue := gjson.GetBytes(w.pending.Bytes(), "error")
		if w.ctx != nil && errorValue.Exists() && errorValue.Type != gjson.Null && channelRoutingUpstreamRetryable(w.ctx) {
			w.failure = true
			return len(body), nil
		}
		return len(body), w.commit()
	}
	if business {
		return len(body), w.commit()
	}
	return len(body), nil
}

// failedResponse reports an HTTP or protocol failure observed during this attempt.
func (w *channelRoutingAttemptWriter) failedResponse() bool { return w.status >= 400 || w.failure }

// writeCommitted forwards bytes and remembers short writes or downstream failures.
func (w *channelRoutingAttemptWriter) writeCommitted(body []byte) (int, error) {
	n, err := w.ResponseWriter.Write(body)
	if err == nil && n < len(body) {
		err = io.ErrShortWrite
	}
	if err != nil && w.writeErr == nil {
		w.writeErr = err
	}
	return n, err
}

// observeSSE classifies complete frames across arbitrary write and delimiter splits.
// Incomplete tails remain bounded, and oversized frames count as business output.
// Subsequent failure events remain observable after the client response has committed.
func (w *channelRoutingAttemptWriter) observeSSE(body []byte) (business bool) {
	for len(body) > 0 {
		space := channelRoutingPendingLimit - len(w.ssePartial)
		// Preserve three trailing bytes because an SSE CRLF delimiter may cross
		// this write boundary after the frame has exceeded the bounded scan buffer.
		if space == 0 {
			business, w.sseDiscard = true, true
			w.ssePartial = append(w.ssePartial[:0], w.ssePartial[len(w.ssePartial)-3:]...)
			space = channelRoutingPendingLimit - len(w.ssePartial)
		}
		take := min(space, len(body))
		w.ssePartial = append(w.ssePartial, body[:take]...)
		body = body[take:]
		consumed := 0
		for {
			unread := w.ssePartial[consumed:]
			end, delimiter := bytes.Index(unread, []byte("\n\n")), 2
			if crlf := bytes.Index(unread, []byte("\r\n\r\n")); crlf >= 0 && (end < 0 || crlf < end) {
				end, delimiter = crlf, 4
			}
			if end < 0 {
				break
			}
			if w.sseDiscard {
				w.sseDiscard = false
			} else {
				prelude, failure := channelRoutingSSEPrelude(unread[:end])
				business = business || !prelude
				w.failure = w.failure || failure
			}
			consumed += end + delimiter
		}
		if consumed > 0 {
			w.ssePartial = append(w.ssePartial[:0], w.ssePartial[consumed:]...)
		}
	}
	return business
}

// channelRoutingSSEPrelude identifies empty protocol preambles and explicit failure frames.
// Tool deltas, usage and generated content close replay; empty role events and
// heartbeat comments may remain pending until later business output or final commit.
func channelRoutingSSEPrelude(frame []byte) (prelude, failure bool) {
	event := ""
	var data []byte
	for line := range bytes.SplitSeq(frame, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("event:")) {
			event = strings.TrimSpace(string(line[6:]))
		} else if bytes.HasPrefix(line, []byte("data:")) {
			data = append(data, bytes.TrimSpace(line[5:])...)
			data = append(data, '\n')
		}
	}
	if len(bytes.TrimSpace(data)) == 0 && event == "" {
		return true, false
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return true, false
	}
	payload := gjson.ParseBytes(data)
	// Usage observations can precede text. Treat measured tokens as business
	// evidence so another group cannot replay a request with an existing billable result.
	if channelMeasuredUsage(payload.Get("usage")) || channelMeasuredUsage(payload.Get("message.usage")) || channelMeasuredUsage(payload.Get("usageMetadata")) {
		return false, false
	}

	if event == "" {
		event = payload.Get("type").String()
	}
	switch event {
	case "ping", "response.created", "response.in_progress":
		return true, false
	case "error", "response.failed":
		return true, true
	case "message_start":
		content := payload.Get("message.content")
		return content.IsArray() && len(content.Array()) == 0, false
	case "content_block_start":
		block := payload.Get("content_block")
		kind := block.Get("type").String()
		return kind == "text" && block.Get("text").String() == "" || kind == "thinking" && block.Get("thinking").String() == "" && block.Get("signature").String() == "", false
	}
	if value := payload.Get("error"); value.Exists() && value.Type != gjson.Null {
		return true, true
	}
	if channelRoutingChatRolePrelude(payload) {
		return true, false
	}
	return false, false
}

// channelRoutingChatRolePrelude recognizes empty assistant role deltas in Chat streams.
// Tool calls, finish reasons and any nonempty generated field count as business
// output, preventing another channel from replaying a response already in progress.
func channelRoutingChatRolePrelude(payload gjson.Result) bool {
	choices := payload.Get("choices")
	if !choices.IsArray() || len(choices.Array()) == 0 {
		return false
	}
	for _, choice := range choices.Array() {
		if reason := choice.Get("finish_reason"); reason.Exists() && reason.Type != gjson.Null {
			return false
		}
		delta := choice.Get("delta")
		if !delta.IsObject() {
			return false
		}
		empty := true
		delta.ForEach(func(key, value gjson.Result) bool {
			switch key.String() {
			case "role":
				empty = value.String() == "assistant" || value.String() == ""
			case "content", "reasoning_content", "refusal":
				empty = value.Type == gjson.Null || value.Type == gjson.String && value.String() == ""
			default:
				empty = false
			}
			return empty
		})
		if !empty {
			return false
		}
	}
	return true
}

// Flush forwards flushing after business commitment while keeping uncommitted errors pending.
func (w *channelRoutingAttemptWriter) Flush() {
	w.WriteHeaderNow()
	if w.committed {
		w.ResponseWriter.Flush()
	}
}

// commit publishes one attempt's complete headers and pending bytes to its parent.
// Repeated calls return the retained write error. The caller serializes commit
// with response writes and discards prior attempts before selecting another channel.
func (w *channelRoutingAttemptWriter) commit() error {
	if w.committed {
		return w.writeErr
	}
	w.committed = true
	dst := w.ResponseWriter.Header()
	for key := range dst {
		delete(dst, key)
	}
	for key, values := range w.header {
		dst[key] = append([]string(nil), values...)
	}
	w.ResponseWriter.WriteHeader(w.status)
	if w.pending.Len() > 0 {
		_, w.writeErr = io.Copy(w.ResponseWriter, &w.pending)
	}
	return w.writeErr
}

// channelMeasuredUsage reports positive scalar usage in a protocol's measurement object.
// Nested detail counters participate while string labels remain metadata. Empty
// objects and zero counters do not independently close an otherwise safe replay.
func channelMeasuredUsage(value gjson.Result) bool {
	measured := false
	value.ForEach(func(_ gjson.Result, item gjson.Result) bool {
		measured = item.Type == gjson.Number && item.Float() > 0
		if item.IsObject() {
			measured = channelMeasuredUsage(item)
		}
		return !measured
	})
	return measured
}
