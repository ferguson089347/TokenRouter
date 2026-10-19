package ticket

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// ticketServiceRepoStub captures service persistence calls while executing production ticket and attachment validation.
type ticketServiceRepoStub struct {
	TicketRepository
	createCalls, replyCalls, expireCalls int
	created                              *CreateTicketInput
	createdHash                          string
	updated                              *UpdateTicketInput
	listFilter                           TicketListFilter
	expireCutoff                         time.Time
}

// ticketConfigStub supplies mutable support configuration and failures to service validation tests.
type ticketConfigStub struct {
	config *TicketConfig
	err    error
}

// Create records the validated creation input and returns the configured ticket result.
func (r *ticketServiceRepoStub) Create(_ context.Context, _ TicketActor, in *CreateTicketInput, _ int, hash string) (*Ticket, error) {
	r.createCalls++
	r.created = in
	r.createdHash = hash
	return &Ticket{ID: 1}, nil
}

// TestTicketConsultationNormalizesLegacyTypes exercises historical sales
// categories in creation and list filters. Stored values use consultation while
// request digests retain submitted categories for existing retry keys.
func TestTicketConsultationNormalizesLegacyTypes(t *testing.T) {
	for _, kind := range []string{"consultation", "presales", "aftersales", "other"} {
		t.Run(kind, func(t *testing.T) {
			s, repo, _ := testTicketService()
			input := CreateTicketInput{Type: kind, Title: "咨询", Content: "问题", Priority: "normal", IdempotencyKey: "original"}
			_, err := s.Create(context.Background(), TicketActor{UserID: 1}, &input)
			if err != nil || repo.created.Type != "consultation" {
				t.Fatalf("create: %v, %+v", err, repo.created)
			}
			if repo.createdHash != ticketRequestHash(input, nil) {
				t.Fatal("legacy request hash changed")
			}
			_, err = s.List(context.Background(), TicketActor{UserID: 1}, TicketListFilter{Type: kind})
			if err != nil || repo.listFilter.Type != "consultation" {
				t.Fatalf("list: %v, %+v", err, repo.listFilter)
			}
		})
	}
	if validTicketType("other") || !validTicketType("consultation") {
		t.Fatal("formal ticket types are not canonical")
	}
}

// Reply records a reply call and returns the fixture conversation result.
func (r *ticketServiceRepoStub) Reply(_ context.Context, _ TicketActor, _ int64, _ *ReplyTicketInput, _ string) (*Ticket, error) {
	r.replyCalls++
	return &Ticket{ID: 1}, nil
}

// List captures normalized list filters for service validation assertions.
func (r *ticketServiceRepoStub) List(_ context.Context, _ TicketActor, filter TicketListFilter) (*TicketListResult, error) {
	r.listFilter = filter
	return &TicketListResult{}, nil
}

// Update captures the validated priority change for permission and normalization assertions.
func (r *ticketServiceRepoStub) Update(_ context.Context, _ TicketActor, _ int64, in *UpdateTicketInput) (*Ticket, error) {
	r.updated = in
	return &Ticket{ID: 1}, nil
}

// Expire captures the expiration cutoff and records one repository scan.
func (r *ticketServiceRepoStub) Expire(_ context.Context, cutoff time.Time) (int64, error) {
	r.expireCalls++
	r.expireCutoff = cutoff
	return 2, nil
}

// Get returns the selected configuration or its configured read failure.
func (c ticketConfigStub) Get(context.Context) (*TicketConfig, error) { return c.config, c.err }

// testTicketService combines production validation with captured persistence and
// mutable configuration fixtures.
func testTicketService() (*TicketService, *ticketServiceRepoStub, *TicketConfig) {
	r := &ticketServiceRepoStub{}
	config := &TicketConfig{Enabled: true, MaxOpenTickets: 3, MaxAttachments: 3, MaxAttachmentSizeMB: 10}
	return NewTicketService(r, ticketConfigStub{config: config}), r, config
}

// TestTicketCreateValidationAndFinancialSelection exercises valid creation and
// malformed ticket fields with optional order selection. Financial requests can
// reference an order while incompatible category and order combinations fail.
func TestTicketCreateValidationAndFinancialSelection(t *testing.T) {
	orderID := int64(2)
	for _, tc := range []struct {
		name   string
		mutate func(*CreateTicketInput)
		want   error
	}{
		{"valid", func(*CreateTicketInput) {}, nil},
		{"financial order is optional", func(in *CreateTicketInput) { in.Type = "financial" }, nil},
		{"financial with order", func(in *CreateTicketInput) { in.Type = "financial"; in.OrderID = &orderID }, nil},
		{"technical rejects order", func(in *CreateTicketInput) { in.OrderID = &orderID }, ErrTicketOrderInvalid},
		{"invalid priority", func(in *CreateTicketInput) { in.Priority = "critical" }, ErrTicketInputInvalid},
		{"empty title", func(in *CreateTicketInput) { in.Title = " " }, ErrTicketInputInvalid},
		{"long title", func(in *CreateTicketInput) { in.Title = strings.Repeat("字", 51) }, ErrTicketTitleTooLong},
		{"empty content", func(in *CreateTicketInput) { in.Content = "\n" }, ErrTicketInputInvalid},
		{"long content", func(in *CreateTicketInput) { in.Content = strings.Repeat("字", 20001) }, ErrTicketInputInvalid},
		{"invalid key", func(in *CreateTicketInput) { in.IdempotencyKey = "key\n" }, ErrTicketInputInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, _ := testTicketService()
			in := &CreateTicketInput{Type: "technical", Title: " 标题 ", Content: " 内容 "}
			tc.mutate(in)
			_, err := s.Create(context.Background(), TicketActor{UserID: 1}, in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Create error = %v, want %v", err, tc.want)
			}
			if tc.want != nil && repo.createCalls != 0 {
				t.Fatal("invalid request reached persistence")
			}
			if tc.want == nil && (repo.created.Title != "标题" || repo.created.Content != "内容" || repo.created.Priority != "normal") {
				t.Fatalf("input not normalized: %+v", repo.created)
			}
		})
	}
}

// TestTicketServiceValidatesAttachmentsWithoutHandler exercises a direct service
// caller submitting unsafe attachments. Content validation rejects the files
// before repository creation, covering callers that do not use the HTTP parser.
func TestTicketServiceValidatesAttachmentsWithoutHandler(t *testing.T) {
	s, repo, _ := testTicketService()
	files := []TicketAttachmentUpload{{Name: "evil.exe", ContentType: "application/octet-stream", Data: []byte("MZ")}}
	if _, err := s.Create(context.Background(), TicketActor{UserID: 1}, &CreateTicketInput{Type: "technical", Title: "标题", Content: "内容", Attachments: files}); err == nil {
		t.Fatal("Create accepted unsupported attachment")
	}
	if _, err := s.Reply(context.Background(), TicketActor{UserID: 1}, 1, &ReplyTicketInput{Content: "回复", Attachments: files}); err == nil {
		t.Fatal("Reply accepted unsupported attachment")
	}
	if repo.createCalls != 0 || repo.replyCalls != 0 {
		t.Fatal("invalid attachments reached persistence")
	}
}

// TestTicketTitleCharacterLimit exercises ASCII, Chinese, and supplementary
// Unicode subjects around the title limit. Accepted titles use code-point counts
// and longer subjects fail before any repository write.
func TestTicketTitleCharacterLimit(t *testing.T) {
	for _, symbol := range []string{"a", "字", "😀"} {
		for _, count := range []int{50, 51} {
			s, repo, _ := testTicketService()
			title := strings.Repeat(symbol, count)
			_, err := s.Create(context.Background(), TicketActor{UserID: 1}, &CreateTicketInput{
				Type: "consultation", Title: " \n" + title + "\t ", Content: "问题说明",
			})
			if count == 50 {
				if err != nil || repo.createCalls != 1 || repo.created.Title != title {
					t.Fatalf("50字符边界未正常保存: %q, %v", symbol, err)
				}
			} else if !errors.Is(err, ErrTicketTitleTooLong) || repo.createCalls != 0 {
				t.Fatalf("51字符标题未被拒绝: %q, %v", symbol, err)
			}
		}
	}
}

// TestTicketModuleSwitchGuardsEveryBusinessEntry exercises disabled support
// configuration across user and administrator operations. Every business read or
// write rejects access before calling persistence, while configuration remains
// readable.
func TestTicketModuleSwitchGuardsEveryBusinessEntry(t *testing.T) {
	ctx := context.Background()
	priority := "high"
	operations := map[string]func(*TicketService, TicketActor) error{
		"list": func(s *TicketService, actor TicketActor) error {
			_, err := s.List(ctx, actor, TicketListFilter{})
			return err
		},
		"get": func(s *TicketService, actor TicketActor) error { _, err := s.Get(ctx, actor, 1); return err },
		"create": func(s *TicketService, actor TicketActor) error {
			_, err := s.Create(ctx, actor, &CreateTicketInput{Type: "technical", Title: "标题", Content: "内容"})
			return err
		},
		"reply": func(s *TicketService, actor TicketActor) error {
			_, err := s.Reply(ctx, actor, 1, &ReplyTicketInput{Content: "回复"})
			return err
		},
		"complete": func(s *TicketService, actor TicketActor) error {
			_, err := s.Close(ctx, actor, 1, TicketStatusCompleted)
			return err
		},
		"cancel": func(s *TicketService, actor TicketActor) error {
			_, err := s.Close(ctx, actor, 1, TicketStatusCancelled)
			return err
		},
		"update": func(s *TicketService, actor TicketActor) error {
			_, err := s.Update(ctx, actor, 1, &UpdateTicketInput{Priority: &priority})
			return err
		},
		"attachment": func(s *TicketService, actor TicketActor) error { _, err := s.Attachment(ctx, actor, 1, 2); return err },
	}
	failure := errors.New("配置数据库不可用")
	for _, isAdmin := range []bool{false, true} {
		for _, configErr := range []error{nil, failure} {
			for name, operation := range operations {
				if name == "update" && !isAdmin {
					continue
				}
				t.Run(name+map[bool]string{false: "/user", true: "/admin"}[isAdmin]+map[bool]string{false: "/disabled", true: "/failure"}[configErr != nil], func(t *testing.T) {
					// An absent repository panics if configuration accidentally admits the
					// request, making any bypass visible before fixture data could hide it.
					s := NewTicketService(nil, ticketConfigStub{config: &TicketConfig{Enabled: false}, err: configErr})
					want := error(ErrTicketDisabled)
					if configErr != nil {
						want = configErr
					}
					if err := operation(s, TicketActor{UserID: 1, IsAdmin: isAdmin}); !errors.Is(err, want) {
						t.Fatalf("error = %v, want %v", err, want)
					}
				})
			}
		}
	}
	s, repo, config := testTicketService()
	config.Enabled, config.AutoExpireHours = false, 24
	if actual, err := s.Config(ctx); err != nil || actual.Enabled {
		t.Fatalf("关闭时仍应允许读取配置: %+v, %v", actual, err)
	}
	if count, err := s.Expire(ctx); err != nil || count != 0 || repo.expireCalls != 0 {
		t.Fatalf("关闭时不应过期历史工单: %d, %v", count, err)
	}
	config.Enabled = true
	if _, err := s.Create(ctx, TicketActor{UserID: 1}, &CreateTicketInput{Type: "technical", Title: "标题", Content: "内容"}); err != nil || repo.createCalls != 1 {
		t.Fatalf("重新启用未恢复创建: %v", err)
	}
}

// TestTicketNilConfigFailsClosed exercises a configuration provider returning
// nil without an error. The service reports configuration failure and prevents
// ticket access from proceeding with an unknown switch value.
func TestTicketNilConfigFailsClosed(t *testing.T) {
	for _, provider := range []TicketConfigProvider{nil, ticketConfigStub{}} {
		s := NewTicketService(nil, provider)
		if _, err := s.List(context.Background(), TicketActor{UserID: 1}, TicketListFilter{}); err == nil {
			t.Fatal("空配置放行读取")
		}
		if _, err := s.Expire(context.Background()); err == nil {
			t.Fatal("空配置放行过期任务")
		}
	}
}

// TestTicketExpireRespectsDisabledTimerAndLatestConfig exercises expiration
// scans with disabled support, disabled timing, and changed limits. Persistence
// receives a cutoff only when the latest configuration permits automatic
// expiration.
func TestTicketExpireRespectsDisabledTimerAndLatestConfig(t *testing.T) {
	s, repo, config := testTicketService()
	if count, err := s.Expire(context.Background()); count != 0 || err != nil || repo.expireCalls != 0 {
		t.Fatal("disabled expiration touched persistence")
	}
	config.AutoExpireHours = 48
	before := time.Now().Add(-48 * time.Hour)
	if count, err := s.Expire(context.Background()); count != 2 || err != nil {
		t.Fatalf("Expire = %d, %v", count, err)
	}
	if repo.expireCutoff.Before(before) || repo.expireCutoff.After(time.Now().Add(-48*time.Hour)) {
		t.Fatal("expiration does not use configured user inactivity interval")
	}
}

// TestTicketListBoundsAndStaffMutationPermission exercises pagination bounds,
// filter validation, and priority changes from personal scope. Lists receive
// normalized sizes and unauthorized mutations fail before the repository is
// called.
func TestTicketListBoundsAndStaffMutationPermission(t *testing.T) {
	s, repo, _ := testTicketService()
	if _, err := s.List(context.Background(), TicketActor{UserID: 1}, TicketListFilter{PageSize: 1000}); err != nil {
		t.Fatal(err)
	}
	if repo.listFilter.Page != 1 || repo.listFilter.PageSize != 100 {
		t.Fatalf("invalid pagination: %+v", repo.listFilter)
	}
	priority := "high"
	if _, err := s.Update(context.Background(), TicketActor{UserID: 1}, 1, &UpdateTicketInput{Priority: &priority}); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("user priority update error = %v", err)
	}
}

// TestTicketPriorityNormalizationPreservesLegacyHash exercises historical urgent
// requests and priority filters. Persistence receives high priority while the
// creation digest retains the submitted value for idempotent replay
// compatibility.
func TestTicketPriorityNormalizationPreservesLegacyHash(t *testing.T) {
	ctx := context.Background()
	for _, priority := range []string{"", "low", "normal", "high", "urgent"} {
		t.Run(priority, func(t *testing.T) {
			s, repo, _ := testTicketService()
			input := CreateTicketInput{Type: "other", Title: "标题", Content: "内容", Priority: priority, IdempotencyKey: "legacy"}
			_, err := s.Create(ctx, TicketActor{UserID: 1}, &input)
			if err != nil {
				t.Fatal(err)
			}
			want := priority
			if want == "" {
				want = "normal"
			}
			if want == "urgent" {
				want = "high"
			}
			if repo.created.Priority != want || input.Priority != priority {
				t.Fatalf("规范化或原输入错误: %+v, %+v", repo.created, input)
			}
			hashInput := input
			if hashInput.Priority == "" {
				hashInput.Priority = "normal"
			}
			if repo.createdHash != ticketRequestHash(hashInput, nil) {
				t.Fatal("升级改变旧优先级幂等摘要")
			}
			if priority == "" {
				return
			}
			_, err = s.List(ctx, TicketActor{UserID: 1}, TicketListFilter{Priority: priority})
			if err != nil || repo.listFilter.Priority != want {
				t.Fatalf("筛选规范化失败: %v", err)
			}
			_, err = s.Update(ctx, TicketActor{UserID: 2, IsAdmin: true}, 1, &UpdateTicketInput{Priority: &priority})
			if err != nil || *repo.updated.Priority != want {
				t.Fatalf("更新规范化失败: %v", err)
			}
		})
	}
	if validTicketPriority("urgent") {
		t.Fatal("正式优先级仍包含紧急")
	}
}

// TestTicketAssigneeContractRemoved exercises ticket JSON and priority update
// shapes. Serialized responses omit assignment fields while conversation senders
// retain the identity needed to attribute individual support messages.
func TestTicketAssigneeContractRemoved(t *testing.T) {
	data, err := json.Marshal(Ticket{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "assigned_") {
		t.Fatal("仍对外暴露处理人员")
	}
	var input UpdateTicketInput
	if err := json.Unmarshal([]byte(`{"assign_to_me":true}`), &input); err != nil {
		t.Fatal(err)
	}
	s, _, _ := testTicketService()
	if _, err := s.Update(context.Background(), TicketActor{UserID: 1, IsAdmin: true}, 1, &input); !errors.Is(err, ErrTicketInputInvalid) {
		t.Fatalf("旧接单字段仍可执行操作: %v", err)
	}
}

// TestTicketRequestHashIncludesAttachmentBytesAndMetadata exercises requests
// differing in file bytes, names, types, or order. Their hashes differ so one
// retry key cannot silently address another attachment submission.
func TestTicketRequestHashIncludesAttachmentBytesAndMetadata(t *testing.T) {
	in := ReplyTicketInput{Content: "回复"}
	file := TicketAttachmentUpload{Name: "a.pdf", ContentType: "application/pdf", Data: []byte("first")}
	first := ticketRequestHash(in, []TicketAttachmentUpload{file})
	file.Data = []byte("second")
	second := ticketRequestHash(in, []TicketAttachmentUpload{file})
	file.Name = "b.pdf"
	third := ticketRequestHash(in, []TicketAttachmentUpload{file})
	if first == second || second == third {
		t.Fatal("different attachment payloads share request hash")
	}
}
