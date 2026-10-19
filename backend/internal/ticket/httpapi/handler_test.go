package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	support "github.com/TokenFlux/TokenRouter/internal/ticket"
)

// ticketHandlerSettings supplies raw support settings to the production handler configuration reader.
type ticketHandlerSettings struct {
	support.ConfigStore
	values map[string]string
}

// ticketHandlerRepo captures authenticated actors and submitted inputs for handler permission assertions.
type ticketHandlerRepo struct {
	support.TicketRepository
	actor          support.TicketActor
	input          *support.CreateTicketInput
	err            error
	closureCreated bool
	attachment     *support.TicketAttachment
}

// ticketHandlerNotifier records closure events emitted after handler persistence results are accepted.
type ticketHandlerNotifier struct {
	closures []*support.Ticket
}

// GetMultiple returns selected raw settings or the configured fixture failure.
func (s *ticketHandlerSettings) GetMultiple(context.Context, []string) (map[string]string, error) {
	return s.values, nil
}

// Close captures the authenticated closing actor and the requested terminal state.
func (r *ticketHandlerRepo) Close(_ context.Context, actor support.TicketActor, id int64, status string) (*support.Ticket, error) {
	r.actor = actor
	return &support.Ticket{ID: id, Status: status, ClosureCreated: r.closureCreated}, r.err
}

// NotifyReply accepts reply notifications unused by the closure-only capture fixture.
func (n *ticketHandlerNotifier) NotifyReply(*support.Ticket) {}

// NotifyClosure records committed closure notifications for handler scope and replay assertions.
func (n *ticketHandlerNotifier) NotifyClosure(ticket *support.Ticket) {
	n.closures = append(n.closures, ticket)
}

// Create records the validated creation input and returns the configured ticket result.
func (r *ticketHandlerRepo) Create(_ context.Context, actor support.TicketActor, in *support.CreateTicketInput, _ int, _ string) (*support.Ticket, error) {
	r.actor = actor
	r.input = in
	return &support.Ticket{ID: 12, UserID: actor.UserID}, r.err
}

// Attachment returns fixture private bytes after recording the actor selected by the handler.
func (r *ticketHandlerRepo) Attachment(_ context.Context, actor support.TicketActor, _, _ int64) (*support.TicketAttachment, error) {
	r.actor = actor
	if r.attachment != nil {
		return r.attachment, r.err
	}
	return &support.TicketAttachment{Filename: "账单.pdf", ContentType: "application/pdf", Data: []byte("document")}, r.err
}

// ticketHandlerTestRouter mounts one handler with selected authenticated
// identity and live ticket configuration. Callers choose the route and action so
// the fixture exercises the same HTTP parser under both access scopes.
func ticketHandlerTestRouter(repo *ticketHandlerRepo, values map[string]string, role string, authenticated bool, method, path string, action func(*TicketHandler) gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	cfg := support.NewTicketConfigService(&ticketHandlerSettings{values: values})
	h := NewTicketHandler(support.NewTicketService(repo, cfg), cfg, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if authenticated {
			c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 7})
			c.Set(string(authctx.ContextKeyUserRole), role)
		}
	})
	r.Handle(method, path, action(h))
	return r
}

// ticketMultipart builds one payload and a requested number of valid PDF
// attachments.
func ticketMultipart(t *testing.T, payload string, files int) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	require.NoError(t, writer.WriteField("payload", payload))
	for range files {
		part, err := writer.CreateFormFile("files", "receipt.pdf")
		require.NoError(t, err)
		_, err = part.Write(ticketHandlerPDF(0))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return body, writer.FormDataContentType()
}

// ticketHandlerPDF builds a valid static PDF with optional padding for multipart
// spill tests.
func ticketHandlerPDF(padding int) []byte {
	var out bytes.Buffer
	_, _ = out.WriteString("%PDF-1.7\n")
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", padding, strings.Repeat(" ", padding)),
	}
	offsets := make([]int, len(objects))
	for i, object := range objects {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := out.Len()
	fmt.Fprint(&out, "xref\n0 5\n0000000000 65535 f \n")
	for _, offset := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return out.Bytes()
}

// TestTicketHandlerCreateMultipartUsesAuthenticatedOwner exercises multipart
// creation containing a forged owner field. The handler uses the authenticated
// account and forwards validated files and request identity to the ticket
// service.
func TestTicketHandlerCreateMultipartUsesAuthenticatedOwner(t *testing.T) {
	repo := &ticketHandlerRepo{}
	r := ticketHandlerTestRouter(repo, nil, "admin", true, "POST", "/tickets", func(h *TicketHandler) gin.HandlerFunc { return h.Create })
	body, contentType := ticketMultipart(t, `{"type":"technical","title":"调用异常","content":"详细问题","priority":"high","user_id":999,"is_admin":true}`, 1)
	req := httptest.NewRequest(http.MethodPost, "/tickets", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Idempotency-Key", "create-test-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, support.TicketActor{UserID: 7}, repo.actor)
	require.Equal(t, "create-test-1", repo.input.IdempotencyKey)
	require.Len(t, repo.input.Attachments, 1)
	require.Equal(t, "application/pdf", repo.input.Attachments[0].ContentType)
}

// TestTicketHandlerUploadLimitsAndMalformedPayload exercises malformed multipart
// forms, excessive files, and oversized data. The handler rejects complete
// requests before calling ticket creation and preserves the public attachment
// error reasons.
func TestTicketHandlerUploadLimitsAndMalformedPayload(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		files         int
		settings      map[string]string
	}{
		{"禁用附件", `{"type":"technical","title":"a","content":"b","priority":"normal"}`, 1, map[string]string{support.SettingKeyTicketMaxAttachments: "0"}},
		{"超过数量", `{}`, 2, map[string]string{support.SettingKeyTicketMaxAttachments: "1"}},
		{"错误正文", `invalid-json`, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &ticketHandlerRepo{}
			r := ticketHandlerTestRouter(repo, tc.settings, "user", true, "POST", "/tickets", func(h *TicketHandler) gin.HandlerFunc { return h.Create })
			body, contentType := ticketMultipart(t, tc.payload, tc.files)
			req := httptest.NewRequest(http.MethodPost, "/tickets", body)
			req.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 400, w.Code)
			require.Nil(t, repo.input)
		})
	}
}

// TestTicketHandlerRejectsLongTitle exercises JSON and multipart submissions
// with excessive Unicode titles. Both request formats return the title-limit
// reason and leave the ticket repository creation count unchanged.
func TestTicketHandlerRejectsLongTitle(t *testing.T) {
	payload, err := json.Marshal(map[string]string{"type": "technical", "title": strings.Repeat("字", 51), "content": "问题说明"})
	require.NoError(t, err)
	for _, multipartBody := range []bool{false, true} {
		repo := &ticketHandlerRepo{}
		r := ticketHandlerTestRouter(repo, nil, "user", true, "POST", "/tickets", func(h *TicketHandler) gin.HandlerFunc { return h.Create })
		body, contentType := bytes.NewBuffer(payload), "application/json"
		if multipartBody {
			body, contentType = ticketMultipart(t, string(payload), 0)
		}
		req := httptest.NewRequest(http.MethodPost, "/tickets", body)
		req.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 400, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "TICKET_TITLE_TOO_LONG")
		require.Nil(t, repo.input)
	}
}

// TestTicketHandlerAttachmentAuthAndPrivateHeaders exercises private download
// requests under user and administrator identities. Unauthorized actors are
// rejected, and successful downloads include attachment disposition, no-store,
// anti-sniffing, and sandbox headers.
func TestTicketHandlerAttachmentAuthAndPrivateHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, role  string
		auth, admin bool
		err         error
		status      int
	}{
		{"未登录", "", false, false, nil, 401},
		{"个人下载", "user", true, false, nil, 200},
		{"越权工单", "user", true, false, support.ErrTicketNotFound, 404},
		{"普通用户访问后台", "user", true, true, nil, 403},
		{"管理员下载", "admin", true, true, nil, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &ticketHandlerRepo{err: tc.err}
			r := ticketHandlerTestRouter(repo, nil, tc.role, tc.auth, "GET", "/tickets/:id/attachments/:attachmentId", func(h *TicketHandler) gin.HandlerFunc {
				if tc.admin {
					return h.AdminDownload
				}
				return h.Download
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tickets/12/attachments/9", nil))
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status == 200 {
				require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
				require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
				require.Contains(t, w.Header().Get("Content-Security-Policy"), "sandbox")
				require.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
				require.Contains(t, w.Header().Get("Content-Disposition"), "attachment;")
				require.Equal(t, "document", w.Body.String())
				require.Equal(t, tc.admin, repo.actor.IsAdmin)
			}
		})
	}
}

// TestTicketHandlerOpenLimitPreservesReason exercises a repository refusing
// creation because the owner already has open tickets. The handler keeps the
// stable conflict reason so clients can display localized guidance.
func TestTicketHandlerOpenLimitPreservesReason(t *testing.T) {
	repo := &ticketHandlerRepo{err: support.ErrTicketOpenLimit}
	r := ticketHandlerTestRouter(repo, nil, "user", true, "POST", "/tickets", func(h *TicketHandler) gin.HandlerFunc { return h.Create })
	req := httptest.NewRequest(http.MethodPost, "/tickets", bytes.NewBufferString(`{"type":"technical","title":"a","content":"b","priority":"normal"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusConflict, w.Code)
	var data map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &data))
	require.Equal(t, "TICKET_OPEN_LIMIT", data["reason"])
}

// TestTicketHandlerCloseUsesAuthenticatedActor exercises closing requests
// carrying forged identity or role data. The selected authenticated endpoint
// supplies the recorded actor, including administrators choosing their personal
// ticket entry.
func TestTicketHandlerCloseUsesAuthenticatedActor(t *testing.T) {
	for _, target := range []string{support.TicketStatusCompleted, support.TicketStatusCancelled} {
		for _, tc := range []struct {
			name, role  string
			auth, admin bool
			err         error
			status      int
		}{
			{"未登录", "", false, false, nil, 401},
			{"用户结单", "user", true, false, nil, 200},
			{"管理员个人入口仍是用户", "admin", true, false, nil, 200},
			{"普通用户不能在后台结单", "user", true, true, nil, 403},
			{"客服结单", "admin", true, true, nil, 200},
			{"越权工单", "user", true, false, support.ErrTicketNotFound, 404},
		} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				repo := &ticketHandlerRepo{err: tc.err}
				r := ticketHandlerTestRouter(repo, nil, tc.role, tc.auth, "POST", "/tickets/:id/close", func(h *TicketHandler) gin.HandlerFunc {
					if target == support.TicketStatusCompleted {
						if tc.admin {
							return h.AdminComplete
						}
						return h.Complete
					}
					if tc.admin {
						return h.AdminCancel
					}
					return h.Cancel
				})
				req := httptest.NewRequest(http.MethodPost, "/tickets/12/close", strings.NewReader(`{"user_id":999,"is_admin":true,"closed_by":999,"closed_by_role":"system"}`))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, tc.status, w.Code, w.Body.String())
				if tc.status == http.StatusUnauthorized || tc.status == http.StatusForbidden {
					require.Zero(t, repo.actor.UserID)
					return
				}
				require.Equal(t, support.TicketActor{UserID: 7, IsAdmin: tc.admin}, repo.actor)
				if tc.status == http.StatusOK {
					var result struct {
						Data support.Ticket `json:"data"`
					}
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
					require.Equal(t, target, result.Data.Status)
					require.EqualValues(t, 12, result.Data.ID)
				}
			})
		}
	}
}

// TestTicketHandlerClosureNotificationBoundary exercises first and repeated
// terminal actions under both entry scopes. Only newly committed administrator
// closures enqueue support mail, while persistence errors leave notifications
// untouched.
func TestTicketHandlerClosureNotificationBoundary(t *testing.T) {
	for _, target := range []string{support.TicketStatusCompleted, support.TicketStatusCancelled} {
		for _, tc := range []struct {
			name           string
			admin, created bool
			err            error
			want           int
		}{
			{"客服新关单", true, true, nil, 1},
			{"重复关单", true, false, nil, 0},
			{"个人入口", false, true, nil, 0},
			{"事务失败", true, true, support.ErrTicketClosed, 0},
		} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				repo := &ticketHandlerRepo{closureCreated: tc.created, err: tc.err}
				notifier := &ticketHandlerNotifier{}
				r := ticketHandlerTestRouter(repo, nil, "admin", true, "POST", "/tickets/:id/close", func(h *TicketHandler) gin.HandlerFunc {
					h.runtime = notifier
					if target == support.TicketStatusCompleted {
						if tc.admin {
							return h.AdminComplete
						}
						return h.Complete
					}
					if tc.admin {
						return h.AdminCancel
					}
					return h.Cancel
				})
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/tickets/12/close", nil))
				if tc.err != nil {
					require.Equal(t, http.StatusConflict, w.Code)
				} else {
					require.Equal(t, http.StatusOK, w.Code)
				}
				require.Len(t, notifier.closures, tc.want)
				if tc.want == 1 {
					require.Equal(t, target, notifier.closures[0].Status)
				}
				require.NotContains(t, w.Body.String(), "ClosureCreated")
				require.NotContains(t, w.Body.String(), "closure_created")
			})
		}
	}
}

// TestTicketHandlerJSONRejectsTrailingOrOversizedBody exercises JSON with a
// second object or oversized trailing bytes. Parsing requires the complete
// bounded body to end after one payload before persistence can run.
func TestTicketHandlerJSONRejectsTrailingOrOversizedBody(t *testing.T) {
	const payload = `{"type":"technical","title":"调用异常","content":"详细问题","priority":"normal"}`
	for _, suffix := range []string{` {"is_admin":true}`, ` invalid`, strings.Repeat(" ", 1<<20)} {
		repo := &ticketHandlerRepo{}
		r := ticketHandlerTestRouter(repo, nil, "user", true, "POST", "/tickets", func(h *TicketHandler) gin.HandlerFunc { return h.Create })
		req := httptest.NewRequest(http.MethodPost, "/tickets", strings.NewReader(payload+suffix))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		require.Nil(t, repo.input)
	}
}

// TestTicketHandlerMultipartRemovesSpilledFiles exercises multipart uploads
// large enough to spill into temporary files. Successful and rejected
// submissions both remove parser-created files before the handler returns to its
// caller.
func TestTicketHandlerMultipartRemovesSpilledFiles(t *testing.T) {
	// Uploads over the parser memory allowance spill into its temporary directory,
	// so both accepted and rejected requests must remove those files afterward.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	for _, scenario := range []string{"成功", "正文损坏", "上传截断"} {
		t.Run(scenario, func(t *testing.T) {
			repo := &ticketHandlerRepo{}
			r := ticketHandlerTestRouter(repo, nil, "user", true, "POST", "/tickets", func(h *TicketHandler) gin.HandlerFunc { return h.Create })
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			payload := `{"type":"technical","title":"调用异常","content":"详细问题","priority":"normal"}`
			if scenario == "正文损坏" {
				payload = "invalid"
			}
			require.NoError(t, writer.WriteField("payload", payload))
			part, err := writer.CreateFormFile("files", "receipt.pdf")
			require.NoError(t, err)
			_, err = part.Write(ticketHandlerPDF(9 << 20))
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			if scenario == "上传截断" {
				closing := bytes.LastIndex(body.Bytes(), []byte("\r\n--"+writer.Boundary()+"--"))
				require.Positive(t, closing)
				body.Truncate(closing)
			}
			req := httptest.NewRequest(http.MethodPost, "/tickets", &body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if scenario == "成功" {
				require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			} else {
				require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			}
			entries, err := os.ReadDir(tmp)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

// TestTicketHandlerRejectsOversizedUploadBeforeConfiguredLimit exercises a
// request exceeding the upload size derived from configuration. The parser
// rejects it before the service can process attachments or call the ticket
// repository.
func TestTicketHandlerRejectsOversizedUploadBeforeConfiguredLimit(t *testing.T) {
	repo := &ticketHandlerRepo{}
	r := ticketHandlerTestRouter(repo, map[string]string{support.SettingKeyTicketMaxAttachments: "0"}, "user", true, "POST", "/tickets", func(h *TicketHandler) gin.HandlerFunc { return h.Create })
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("payload", `{}`))
	part, err := writer.CreateFormFile("files", "large.pdf")
	require.NoError(t, err)
	_, err = part.Write(make([]byte, 2<<20))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	req := httptest.NewRequest(http.MethodPost, "/tickets", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code, w.Body.String())
	require.Nil(t, repo.input)
}

// TestTicketPreviewAuthenticationAndHeaders exercises private attachment
// previews from anonymous, owner, and administrator contexts. Authentication,
// module availability, and ownership failures reject access while successful
// responses carry restrictive private headers.
func TestTicketPreviewAuthenticationAndHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, role            string
		auth, admin, disabled bool
		err                   error
		status                int
	}{
		{"匿名", "", false, false, false, nil, 401},
		{"本人", "user", true, false, false, nil, 200},
		{"跨用户", "user", true, false, false, support.ErrTicketNotFound, 404},
		{"普通用户后台", "user", true, true, false, nil, 403},
		{"客服", "admin", true, true, false, nil, 200},
		{"管理员个人入口", "admin", true, false, false, nil, 200},
		{"功能关闭", "user", true, false, true, nil, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := ticketHandlerPDF(0)
			repo := &ticketHandlerRepo{err: tc.err, attachment: &support.TicketAttachment{Filename: "账单.pdf", ContentType: "application/pdf", Data: data}}
			settings := map[string]string{}
			if tc.disabled {
				settings[support.SettingKeyTicketEnabled] = "false"
			}
			r := ticketHandlerTestRouter(repo, settings, tc.role, tc.auth, "GET", "/tickets/:id/attachments/:attachmentId/preview", func(h *TicketHandler) gin.HandlerFunc {
				if tc.admin {
					return h.AdminPreview
				}
				return h.Preview
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tickets/12/attachments/9/preview", nil))
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
			if tc.status != http.StatusOK {
				require.NotContains(t, w.Body.String(), "%PDF-")
				return
			}
			require.Equal(t, data, w.Body.Bytes())
			require.Equal(t, "application/pdf", w.Header().Get("Content-Type"))
			disposition, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
			require.NoError(t, err)
			require.Equal(t, "inline", disposition)
			require.Equal(t, "账单.pdf", params["filename"])
			require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
			require.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
			require.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
			require.Contains(t, w.Header().Get("Content-Security-Policy"), "sandbox")
			require.Equal(t, support.TicketActor{UserID: 7, IsAdmin: tc.admin}, repo.actor)
		})
	}
}

// TestTicketPreviewWordIsPlainTextAndOriginalStillDownloads exercises an
// authorized Word attachment through preview and original endpoints. Preview
// returns escaped plain text content while downloading still returns the stored
// document bytes.
func TestTicketPreviewWordIsPlainTextAndOriginalStillDownloads(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"word/document.xml":   `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>中文 &lt;script&gt;alert(1)&lt;/script&gt;</w:t></w:r></w:p></w:body></w:document>`,
	} {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	repo := &ticketHandlerRepo{attachment: &support.TicketAttachment{Filename: "说明.docx", ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", Data: buf.Bytes()}}
	for _, preview := range []bool{true, false} {
		r := ticketHandlerTestRouter(repo, nil, "user", true, "GET", "/tickets/:id/attachments/:attachmentId", func(h *TicketHandler) gin.HandlerFunc {
			if preview {
				return h.Preview
			}
			return h.Download
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tickets/12/attachments/9", nil))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		if preview {
			require.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
			require.Contains(t, w.Body.String(), "中文 <script>alert(1)</script>")
			require.NotContains(t, w.Body.String(), "w:document")
		} else {
			require.Contains(t, w.Header().Get("Content-Disposition"), "attachment;")
			require.Equal(t, buf.Bytes(), w.Body.Bytes())
		}
	}
}

// TestTicketPreviewRejectsUnsafeHistoricalContent exercises stored attachments
// with misleading metadata or dangerous document bytes. Revalidation rejects the
// preview even when the record already exists in the private ticket repository.
func TestTicketPreviewRejectsUnsafeHistoricalContent(t *testing.T) {
	repo := &ticketHandlerRepo{attachment: &support.TicketAttachment{Filename: "receipt.pdf", ContentType: "application/pdf", Data: []byte("<script>inert-preview-test</script>")}}
	r := ticketHandlerTestRouter(repo, nil, "user", true, "GET", "/tickets/:id/attachments/:attachmentId/preview", func(h *TicketHandler) gin.HandlerFunc { return h.Preview })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tickets/12/attachments/9/preview", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "TICKET_ATTACHMENT_INVALID")
	require.NotContains(t, w.Body.String(), "inert-preview-test")
}
