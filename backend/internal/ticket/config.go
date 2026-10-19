package ticket

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// config.go reads and validates ticket settings for submissions, private
// reads, and background work. The settings table stores raw strings, while the
// ticket module defines defaults and allowed ranges. Missing or malformed
// individual values select bounded defaults; storage failures prevent callers
// from proceeding with an unknown availability decision.
//
// Updates distinguish omitted fields from explicit false or zero values and
// write only supplied settings in one batch. Concurrent updates to unrelated
// fields therefore do not replace each other. After persistence succeeds, an
// application callback publishes the update to public settings and HTML
// injection caches. Failed writes leave the callback uncalled.

// Ticket setting keys identify independently updated support limits and feature switches in the shared raw settings store.
const (
	SettingKeyTicketEnabled             = "ticket_enabled"
	SettingKeyTicketMaxOpenTickets      = "ticket_max_open_tickets"
	SettingKeyTicketMaxAttachments      = "ticket_max_attachments"
	SettingKeyTicketMaxAttachmentSizeMB = "ticket_max_attachment_size_mb"
	SettingKeyTicketNotifyOnStaffReply  = "ticket_notify_on_staff_reply"
	SettingKeyTicketAutoExpireHours     = "ticket_auto_expire_hours"
)

// TicketConfig defines current availability, submission limits, notification
// policy, and expiration age.
// @project-doc docs/domains/support_tickets.md#ticket_configuration
type TicketConfig struct {
	Enabled             bool `json:"enabled"`                // support module switch
	MaxOpenTickets      int  `json:"max_open_tickets"`       // per-user open ticket limit
	MaxAttachments      int  `json:"max_attachments"`        // per-submission file count limit
	MaxAttachmentSizeMB int  `json:"max_attachment_size_mb"` // per-file size in MiB
	NotifyOnStaffReply  bool `json:"notify_on_staff_reply"`  // support event email switch
	AutoExpireHours     int  `json:"auto_expire_hours"`      // waiting-user expiration age in hours
}

// UpdateTicketConfigInput distinguishes omitted settings from explicit false
// and zero values.
type UpdateTicketConfigInput struct {
	Enabled             *bool `json:"enabled"`                // support module switch
	MaxOpenTickets      *int  `json:"max_open_tickets"`       // per-user open ticket limit
	MaxAttachments      *int  `json:"max_attachments"`        // per-submission file count limit
	MaxAttachmentSizeMB *int  `json:"max_attachment_size_mb"` // per-file size in MiB
	NotifyOnStaffReply  *bool `json:"notify_on_staff_reply"`  // support event email switch
	AutoExpireHours     *int  `json:"auto_expire_hours"`      // waiting-user expiration age in hours
}

// TicketConfigService reads fresh settings and publishes successfully
// committed administrator updates.
type TicketConfigService struct {
	settingRepo ConfigStore // atomic raw settings store
	onUpdate    func()      // post-commit publication callback
}

// ConfigStore persists raw ticket settings with atomic batch updates.
type ConfigStore interface {
	// GetMultiple returns saved values; absent keys are omitted from the map.
	GetMultiple(context.Context, []string) (map[string]string, error)
	// SetMultiple commits the complete settings batch or reports a write failure.
	SetMultiple(context.Context, map[string]string) error
}

// NewTicketConfigService binds ticket configuration to its atomic settings
// storage.
func NewTicketConfigService(settingRepo ConfigStore) *TicketConfigService {
	return &TicketConfigService{settingRepo: settingRepo}
}

// defaultTicketConfig returns an independent configuration with the documented
// support defaults.
func defaultTicketConfig() *TicketConfig {
	return &TicketConfig{
		Enabled: true, MaxOpenTickets: 3, MaxAttachments: 5,
		MaxAttachmentSizeMB: 10, NotifyOnStaffReply: false, AutoExpireHours: 72,
	}
}

// Get reads current settings and parses each value within its documented
// range. Missing or malformed fields use bounded defaults, while database
// failures return errors to every business caller.
func (s *TicketConfigService) Get(ctx context.Context) (*TicketConfig, error) {
	if s == nil || s.settingRepo == nil {
		return nil, fmt.Errorf("工单配置服务不可用")
	}
	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyTicketEnabled, SettingKeyTicketMaxOpenTickets, SettingKeyTicketMaxAttachments,
		SettingKeyTicketMaxAttachmentSizeMB, SettingKeyTicketNotifyOnStaffReply, SettingKeyTicketAutoExpireHours,
	})
	if err != nil {
		return nil, fmt.Errorf("读取工单配置失败: %w", err)
	}
	cfg := defaultTicketConfig()
	cfg.Enabled = parseTicketConfigBool(values[SettingKeyTicketEnabled], cfg.Enabled)
	cfg.MaxOpenTickets = parseTicketConfigInt(values[SettingKeyTicketMaxOpenTickets], 1, 100, cfg.MaxOpenTickets)
	cfg.MaxAttachments = parseTicketConfigInt(values[SettingKeyTicketMaxAttachments], 0, 10, cfg.MaxAttachments)
	cfg.MaxAttachmentSizeMB = parseTicketConfigInt(values[SettingKeyTicketMaxAttachmentSizeMB], 1, 20, cfg.MaxAttachmentSizeMB)
	cfg.NotifyOnStaffReply = parseTicketConfigBool(values[SettingKeyTicketNotifyOnStaffReply], cfg.NotifyOnStaffReply)
	cfg.AutoExpireHours = parseTicketConfigInt(values[SettingKeyTicketAutoExpireHours], 0, 8760, cfg.AutoExpireHours)
	return cfg, nil
}

// Update validates supplied ticket settings and commits their raw values as
// one batch. Nil input returns the current configuration, and nil fields
// remain unchanged. Explicit false and zero are accepted where the
// corresponding setting permits them.
//
// The batch contains only fields present in this request, keeping independent
// concurrent edits from overwriting unrelated settings. Public configuration
// publication runs after persistence succeeds. A write error leaves the
// publication callback uncalled and returns an error to the administrator.
func (s *TicketConfigService) Update(ctx context.Context, input *UpdateTicketConfigInput) (*TicketConfig, error) {
	cfg, err := s.Get(ctx)
	if err != nil {
		return nil, err
	}
	if input == nil {
		return cfg, nil
	}
	updates := make(map[string]string, 6)
	for _, field := range []struct {
		key      string
		value    *int
		target   *int
		min, max int
	}{
		{SettingKeyTicketMaxOpenTickets, input.MaxOpenTickets, &cfg.MaxOpenTickets, 1, 100},
		{SettingKeyTicketMaxAttachments, input.MaxAttachments, &cfg.MaxAttachments, 0, 10},
		{SettingKeyTicketMaxAttachmentSizeMB, input.MaxAttachmentSizeMB, &cfg.MaxAttachmentSizeMB, 1, 20},
		{SettingKeyTicketAutoExpireHours, input.AutoExpireHours, &cfg.AutoExpireHours, 0, 8760},
	} {
		if field.value == nil {
			continue
		}
		if *field.value < field.min || *field.value > field.max {
			return nil, apperror.BadRequest("TICKET_CONFIG_INVALID", fmt.Sprintf("%s 必须在 %d 到 %d 之间", field.key, field.min, field.max))
		}
		*field.target = *field.value
		updates[field.key] = strconv.Itoa(*field.value)
	}
	if input.Enabled != nil {
		cfg.Enabled = *input.Enabled
		updates[SettingKeyTicketEnabled] = strconv.FormatBool(*input.Enabled)
	}
	if input.NotifyOnStaffReply != nil {
		cfg.NotifyOnStaffReply = *input.NotifyOnStaffReply
		updates[SettingKeyTicketNotifyOnStaffReply] = strconv.FormatBool(*input.NotifyOnStaffReply)
	}
	if len(updates) > 0 {
		if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
			return nil, fmt.Errorf("保存工单配置失败: %w", err)
		}
		// Publish after the batch commit so public navigation and HTML injection
		// cannot advertise an update that failed to persist.
		if s.onUpdate != nil {
			s.onUpdate()
		}
	}
	return cfg, nil
}

// parseTicketConfigInt selects a bounded integer or the supplied safe default.
// Surrounding whitespace is accepted, while malformed and out-of-range values
// return the caller-defined default used for absent settings.
func parseTicketConfigInt(raw string, min, max, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < min || value > max {
		return fallback
	}
	return value
}

// parseTicketConfigBool parses a stored boolean or returns the supplied
// default. Surrounding whitespace is accepted, while malformed or absent text
// uses the module default for that field.
func parseTicketConfigBool(raw string, fallback bool) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return value
}

// SetOnUpdate installs configuration publication before requests begin using this service.
// The callback runs after successful batch persistence. Application assembly supplies
// settings notification so HTML injection and public configuration readers discard cached data.
func (s *TicketConfigService) SetOnUpdate(fn func()) { s.onUpdate = fn }
