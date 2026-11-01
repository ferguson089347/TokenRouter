package payment

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// recharge_bonus_localization_test.go verifies revisioned activity text and its
// conditional payment save. Sources and translations share stable question IDs,
// while a sequential settings fixture injects a competing write immediately
// before comparison to prove that an unsuccessful save leaves sibling money
// settings untouched.

// rechargeLocalizationRepository simulates settings operations used by coordinated payment updates.
type rechargeLocalizationRepository struct {
	// Repository supplies unused method declarations. Localization saves invoke
	// only this fixture's batch reader, batch writer, and conditional batch writer.
	settings.Repository
	values map[string]string // sequential fixture setting values
	// beforeCompare runs once before conditional comparison, simulating another
	// instance replacing a record after this update has read and prepared it.
	beforeCompare func()
}

// GetMultiple copies requested records while retaining the distinction between absence and emptiness.
func (r *rechargeLocalizationRepository) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := map[string]string{}
	for _, key := range keys {
		if value, exists := r.values[key]; exists {
			result[key] = value
		}
	}
	return result, nil
}

// SetMultiple stores a complete prepared batch in the sequential settings fixture.
func (r *rechargeLocalizationRepository) SetMultiple(_ context.Context, values map[string]string) error {
	maps.Copy(r.values, values)
	return nil
}

// CompareAndSetMultiple rejects changed expectations before storing any prepared sibling values.
// The optional hook simulates a competing instance immediately before this
// sequential comparison; nil expectations accept absent and empty stored strings.
func (r *rechargeLocalizationRepository) CompareAndSetMultiple(ctx context.Context, values map[string]string, expected map[string]*string) error {
	if r.beforeCompare != nil {
		hook := r.beforeCompare
		r.beforeCompare = nil
		hook()
	}
	for key, before := range expected {
		if before == nil && r.values[key] != "" {
			return locale.ErrConflict
		}
		if before != nil && r.values[key] != *before {
			return locale.ErrConflict
		}
	}
	return r.SetMultiple(ctx, values)
}

// TestRechargeBonusLocalizedContent resolves reviewed offer text through stable FAQ identities.
// A source edit invalidates its existing translation, while incomplete stale
// FAQ text stays editable until the administrator approves a complete replacement.
func TestRechargeBonusLocalizedContent(t *testing.T) {
	en := "en"
	activity := RechargeBonusActivity{
		PageEnabled: true, Slug: "recharge-event", Title: "Untrusted plain title",
		FAQs: []RechargeBonusFAQ{{ID: "question-a", Question: "Plain question", Answer: "Plain answer"}},
	}
	source := map[string]string{
		"title": "Recharge event", "subtitle": "Extra credit", "rules": "Each purchase is independent.", "notice": "Offer details",
		"faq.question-a.question": "How are gifts selected?", "faq.question-a.answer": "The highest eligible tier applies.",
	}
	translated := map[string]string{
		"title": "充值活动", "subtitle": "额外赠送", "rules": "每笔充值独立计算。", "notice": "活动说明",
		"faq.question-a.question": "赠送如何计算？", "faq.question-a.answer": "按本笔充值达到的最高档位计算。",
	}
	input := locale.Update[map[string]string]{
		Content: locale.Content[map[string]string]{
			SourceLocale: &en, Source: source,
			Translations: map[string]locale.Translation[map[string]string]{"zh-Hans": {Value: translated}},
		},
		ReviewedLocales: []string{"zh-Hans"},
	}
	change, err := prepareRechargeBonusContentChange(UpdatePaymentConfigRequest{
		RechargeBonusActivity: &activity, RechargeBonusLocalization: &input,
	}, map[string]string{})
	require.NoError(t, err)
	storedActivity := parseRechargeBonusActivity(change.Values[SettingRechargeBonusActivity])
	require.Equal(t, "Recharge event", storedActivity.Title)
	require.Equal(t, "Offer details", change.Values[SettingRechargeBonusNotice])
	require.Equal(t, "Untrusted plain title", activity.Title)
	cfg := &PaymentConfig{RechargeBonusActivity: storedActivity, RechargeBonusNotice: "Offer details"}
	resolveRechargeBonusContent(cfg, change.Values, "zh")
	require.Equal(t, "充值活动", cfg.RechargeBonusActivity.Title)
	require.Equal(t, "活动说明", cfg.RechargeBonusNotice)
	require.Equal(t, "question-a", cfg.RechargeBonusActivity.FAQs[0].ID)
	require.Equal(t, "赠送如何计算？", cfg.RechargeBonusActivity.FAQs[0].Question)
	require.EqualValues(t, 1, cfg.RechargeBonusLocalization.Revision)
	stale := *cfg.RechargeBonusLocalization
	stale.Source = maps.Clone(stale.Source)
	stale.Source["title"] = "Revised recharge event"
	stale.Source["faq.question-b.question"] = "When is credit issued?"
	stale.Source["faq.question-b.answer"] = "After payment is confirmed."
	storedActivity.FAQs = append(storedActivity.FAQs, RechargeBonusFAQ{ID: "question-b"})
	changed, err := prepareRechargeBonusContentChange(UpdatePaymentConfigRequest{
		RechargeBonusActivity: &storedActivity, RechargeBonusLocalization: &stale,
	}, change.Values)
	require.NoError(t, err)
	updated := &PaymentConfig{RechargeBonusActivity: parseRechargeBonusActivity(changed.Values[SettingRechargeBonusActivity])}
	resolveRechargeBonusContent(updated, changed.Values, "zh-Hans")
	require.Equal(t, "Revised recharge event", updated.RechargeBonusActivity.Title)
	require.Equal(t, "When is credit issued?", updated.RechargeBonusActivity.FAQs[1].Question)
	require.Less(t, updated.RechargeBonusLocalization.Translations["zh-Hans"].SourceRevision, updated.RechargeBonusLocalization.SourceRevision)
	stale.Revision = updated.RechargeBonusLocalization.Revision
	stale.ReviewedLocales = []string{"zh-Hans"}
	_, err = prepareRechargeBonusContentChange(UpdatePaymentConfigRequest{
		RechargeBonusActivity: &storedActivity, RechargeBonusLocalization: &stale,
	}, changed.Values)
	require.Error(t, err)
}

// TestRechargeBonusLocalizedSave rejects stale editors and late conflicting storage writes.
// The complete request uses the shared settings coordinator, and a failed
// content comparison must leave its tier update and source mirrors unapplied.
func TestRechargeBonusLocalizedSave(t *testing.T) {
	en := "en"
	repo := &rechargeLocalizationRepository{values: map[string]string{SettingRechargeBonusTiers: "[]"}}
	service := NewConfigService(nil, settings.New(repo), nil, nil, ConfigurationRuntime{})
	activity := RechargeBonusActivity{PageEnabled: true, Slug: "recharge-event", Title: "Plain title"}
	input := locale.Update[map[string]string]{Content: locale.Content[map[string]string]{
		SourceLocale: &en, Source: map[string]string{"title": "Recharge event", "notice": "Initial offer"},
	}}
	tiers := []RechargeBonusTier{{MinAmount: 100, BonusAmount: 10}}
	request := UpdatePaymentConfigRequest{
		RechargeBonusTiers: &tiers, RechargeBonusActivity: &activity, RechargeBonusLocalization: &input,
	}
	require.NoError(t, service.UpdatePaymentConfig(t.Context(), request))
	before := maps.Clone(repo.values)
	tiers[0].BonusAmount = 20
	require.ErrorIs(t, service.UpdatePaymentConfig(t.Context(), request), locale.ErrConflict)
	require.Equal(t, before, repo.values)
	var saved locale.Content[map[string]string]
	require.NoError(t, json.Unmarshal([]byte(repo.values[SettingRechargeBonusContent]), &saved))
	input.Content = saved
	input.Source = maps.Clone(saved.Source)
	input.Source["title"] = "Updated event"
	repo.beforeCompare = func() { repo.values[SettingRechargeBonusContent] = "competing writer" }
	require.ErrorIs(t, service.UpdatePaymentConfig(t.Context(), request), locale.ErrConflict)
	require.Equal(t, before[SettingRechargeBonusTiers], repo.values[SettingRechargeBonusTiers])
	require.Equal(t, before[SettingRechargeBonusActivity], repo.values[SettingRechargeBonusActivity])
	require.Equal(t, before[SettingRechargeBonusNotice], repo.values[SettingRechargeBonusNotice])
	repo.values[SettingRechargeBonusContent] = before[SettingRechargeBonusContent]
	notice := "Bypassed source edit"
	err := service.UpdatePaymentConfig(t.Context(), UpdatePaymentConfigRequest{RechargeBonusNotice: &notice})
	require.ErrorContains(t, err, "localized content")
	require.Equal(t, before, repo.values)
}
