package intelligence

import (
	"context"
	"time"
)

// community.go admits authenticated users' personal Keys into public drawing
// histories. Callers select their own Key by ID and retain current permission
// for its bound group. The first enabled site drawing supplies its generation
// parameters and gateway address. Generated HTML, screenshots,
// and measurements enter a user-test row visible to authorized group readers.
//
// The repository serializes credential and configuration changes with queued
// work. One selected Key and one generation instruction remain stable until the
// job finishes, while community submission limits bound active work. Model calls
// use ordinary gateway billing, and results are shared with current group readers.
// Each submission takes a fresh copy of the administrator's execution parameters
// and keeps the selected personal credential separate from the site's saved Key.

// CommunityRun queues one public drawing using the caller's personal credential.
// Positive userID must own APIKeyID and currently have access to its active group.
// The first enabled site drawing in repository display order supplies its model,
// protocol, reasoning effort, service tier, prompt, and public gateway address.
//
// Successful results are shared through the group's user-test row. Repository
// admission atomically updates execution settings and creates the queue record,
// so concurrent callers cannot replace an active drawing's Key, model, protocol,
// or prompt. Credentials remain private references resolved again before execution.
func (s *IntelligenceService) CommunityRun(ctx context.Context, userID int64, in IntelligenceSelfTestInput) (*IntelligenceRun, error) {
	if !s.isEnabled(ctx) {
		return nil, ErrIntelligenceDisabled
	}
	if userID <= 0 || in.APIKeyID <= 0 {
		return nil, intelligenceInvalid("请选择自己的有效个人普通 Key")
	}
	key, err := s.keys.GetByID(ctx, in.APIKeyID)
	if err != nil || key == nil || key.GroupID == nil || key.UserID != userID || !intelligenceValidKey(key, *key.GroupID) {
		return nil, intelligenceInvalid("请选择自己的有效个人普通 Key")
	}
	group, err := s.groups.GetByID(ctx, *key.GroupID)
	if err != nil || group == nil || group.Status != "active" {
		return nil, intelligenceInvalid("检测 Key 的分组不可用")
	}
	visible, err := s.visibleGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !visible[group.ID] {
		return nil, ErrIntelligenceNotFound
	}
	configs, err := s.repo.ListConfigs(ctx)
	if err != nil {
		return nil, err
	}
	for _, site := range configs {
		if site.GroupID != group.ID || !site.Enabled || site.Benchmark != "drawing" {
			continue
		}
		base, err := validateIntelligenceBaseURL(site.BaseURL)
		if err != nil {
			return nil, intelligenceInvalid("请管理员检查系统画图检测的公网 HTTPS API 地址")
		}
		config := &IntelligenceConfig{
			Source: "user", GroupID: group.ID, GroupName: group.Name,
			Model: site.Model, Benchmark: "drawing", APIKeyID: key.ID,
			BaseURL: base, Prompt: site.Prompt, Protocol: site.Protocol,
			ReasoningEffort: site.ReasoningEffort, ServiceTier: site.ServiceTier,
			Enabled: true, IntervalMinutes: 60,
		}
		return s.repo.CreateCommunityRun(ctx, config, time.Now().UTC())
	}
	return nil, intelligenceInvalid("此 Key 所在渠道暂无启用的系统画图检测")
}
