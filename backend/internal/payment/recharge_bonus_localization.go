package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// recharge_bonus_localization.go prepares revisioned activity content and selects
// reviewed translations for recharge display. Activity titles, rules, notices,
// and FAQ text share one content revision because the landing page presents one
// offer. FAQ fields use saved identities so editing their order keeps each
// translation attached to its question. The activity record retains source text
// alongside monetary schedules, while public readers resolve ordinary strings
// using the request language.
//
// Content preparation and monetary configuration commit in one conditional
// settings batch. The prior raw content, schedule, and notice records are compared
// atomically, so a stale editor or competing write cannot partially publish a
// different offer. Source changes advance translation review versions through
// locale.Prepare; plain presentation writes must match the saved source content.

// resolveRechargeBonusContent selects activity strings and retains complete editor content.
// The supplied configuration has already validated its monetary schedule. Stored
// content is resolved in language order, while corrupt content uses the source
// strings from that schedule and leaves checkout amounts available.
func resolveRechargeBonusContent(cfg *PaymentConfig, values map[string]string, language string) {
	content := readRechargeBonusContent(values, cfg.RechargeBonusActivity, cfg.RechargeBonusNotice)
	cfg.RechargeBonusLocalization = &locale.Update[map[string]string]{Content: content}
	selected, _ := content.Resolve(language)
	applyRechargeBonusContent(&cfg.RechargeBonusActivity, selected)
	cfg.RechargeBonusNotice = selected["notice"]
}

// readRechargeBonusContent reads the saved block or constructs an unknown-language source.
// Missing or malformed JSON uses the validated activity's source fields. A saved
// empty block keeps its empty content, following the shared localization rules.
func readRechargeBonusContent(values map[string]string, activity RechargeBonusActivity, notice string) locale.Content[map[string]string] {
	if raw := values[SettingRechargeBonusContent]; raw != "" {
		var content locale.Content[map[string]string]
		if json.Unmarshal([]byte(raw), &content) == nil {
			return content
		}
		// Presentation can still render the schedule's source text after content
		// decoding fails; strict write preparation rejects the broken record before replacing it.
	}
	return locale.Original(rechargeBonusSource(activity, notice))
}

// rechargeBonusSource builds a source map using each FAQ's stable saved identity.
func rechargeBonusSource(activity RechargeBonusActivity, notice string) map[string]string {
	fields := map[string]string{
		"title": activity.Title, "subtitle": activity.Subtitle,
		"rules": activity.Rules, "notice": notice,
	}
	for _, faq := range activity.FAQs {
		fields["faq."+faq.ID+".question"] = faq.Question
		fields["faq."+faq.ID+".answer"] = faq.Answer
	}
	return fields
}

// applyRechargeBonusContent copies selected strings into the supplied activity record.
// FAQ text is assigned by saved identity and order stays unchanged. Callers own
// the activity's FAQ slice before invoking this mutating display or source step.
func applyRechargeBonusContent(activity *RechargeBonusActivity, fields map[string]string) {
	activity.Title = fields["title"]
	activity.Subtitle = fields["subtitle"]
	activity.Rules = fields["rules"]
	for i := range activity.FAQs {
		id := activity.FAQs[i].ID
		activity.FAQs[i].Question = fields["faq."+id+".question"]
		activity.FAQs[i].Answer = fields["faq."+id+".answer"]
	}
}

// updateRechargeBonusConfiguration commits presentation edits with their monetary settings.
// The configuration store supplies the shared update coordinator, and the caller
// supplies a partial payment request. Holding the session through reading,
// preparation, and commit preserves ordering against other settings changes in
// this process; raw-value comparisons also reject writes from other instances.
//
// Localization requests use locale.Prepare to check the editor revision and
// translation review instructions. Requests containing plain text alone can
// change schedules only while their presentation matches the saved source. Every
// prepared setting joins the same conditional batch, so any conflict leaves the
// entire offer unchanged.
func (s *ConfigService) updateRechargeBonusConfiguration(ctx context.Context, req UpdatePaymentConfigRequest) error {
	coordinator, ok := s.settingRepo.(interface {
		// Updates returns the shared settings coordinator used for conditional saves.
		Updates() *settings.Updates
	})
	if !ok {
		return fmt.Errorf("recharge content requires coordinated settings storage")
	}
	session, err := coordinator.Updates().Begin(ctx)
	if err != nil {
		return err
	}
	defer session.Close()
	current, err := s.settingRepo.GetMultiple(session.Context(), []string{
		SettingRechargeBonusContent, SettingRechargeBonusActivity, SettingRechargeBonusNotice,
	})
	if err != nil {
		return err
	}
	change, err := prepareRechargeBonusContentChange(req, current)
	if err != nil {
		return err
	}
	// The coordinator checks every expected raw record in the transaction that
	// saves this complete payment batch; failed comparisons leave monetary and translated settings together.
	return session.Commit(change)
}

// prepareRechargeBonusContentChange validates one editor snapshot against saved content.
// Missing localization preserves source strings and rejects direct text changes.
// A supplied localization update controls the source mirrors, while its revision
// and reviewed locales determine whether translations remain displayable.
//
// FAQ identities come from the submitted activity metadata before source text is
// applied. Deleted identities are removed from submitted content maps, preventing
// row movement or deletion from assigning a translation to another question.
// The returned raw expectations cover all three participating records and are
// consumed by the settings transaction together with every supplied payment field.
func prepareRechargeBonusContentChange(req UpdatePaymentConfigRequest, current map[string]string) (settings.PreparedChange, error) {
	activity := parseRechargeBonusActivity(current[SettingRechargeBonusActivity])
	old := readRechargeBonusContent(current, activity, current[SettingRechargeBonusNotice])
	if raw := current[SettingRechargeBonusContent]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &old); err != nil {
			return settings.PreparedChange{}, err
		}
	}
	if req.RechargeBonusActivity != nil {
		activity = *req.RechargeBonusActivity
		activity.FAQs = append([]RechargeBonusFAQ{}, activity.FAQs...)
	}
	if req.RechargeBonusLocalization == nil {
		notice := current[SettingRechargeBonusNotice]
		if req.RechargeBonusNotice != nil {
			notice = *req.RechargeBonusNotice
		}
		if !reflect.DeepEqual(rechargeBonusSource(activity, notice), old.Source) {
			return settings.PreparedChange{}, apperror.BadRequest("LOCALIZATION_REQUIRED", "Edit recharge presentation through localized content.")
		}
	} else {
		input := *req.RechargeBonusLocalization
		for _, faq := range activity.FAQs {
			if !rechargeFAQID.MatchString(faq.ID) {
				return settings.PreparedChange{}, apperror.BadRequest("INVALID_RECHARGE_BONUS", "Localized questions require stable IDs.")
			}
		}
		input.Source = normalizedRechargeBonusFields(input.Source, activity)
		applyRechargeBonusContent(&activity, input.Source)
		normalized, err := NormalizeRechargeBonusActivity(activity)
		if err != nil {
			return settings.PreparedChange{}, apperror.BadRequest("INVALID_RECHARGE_BONUS", err.Error())
		}
		activity = normalized
		// Clone submitted translation maps before pruning deleted FAQ identities.
		// Locale preparation can retain omitted translations, while these supplied values belong to this edit.
		input.Translations = make(map[string]locale.Translation[map[string]string], len(req.RechargeBonusLocalization.Translations))
		reviewed := map[string]bool{}
		for _, language := range input.ReviewedLocales {
			reviewed[locale.Normalize(language)] = true
		}
		for language, translation := range req.RechargeBonusLocalization.Translations {
			translation.Value = normalizedRechargeBonusFields(translation.Value, activity)
			if reviewed[locale.Normalize(language)] {
				if err := validateReviewedRechargeBonusFields(translation.Value, activity); err != nil {
					return settings.PreparedChange{}, err
				}
			}
			input.Translations[language] = translation
		}
		next, err := locale.Prepare(old, input, func(fields map[string]string) error {
			return validateRechargeBonusFields(fields)
		})
		if err != nil {
			return settings.PreparedChange{}, err
		}
		applyRechargeBonusContent(&activity, next.Source)
		notice := next.Source["notice"]
		req.RechargeBonusActivity = &activity
		req.RechargeBonusNotice = &notice
		req.RechargeBonusLocalization = nil
		values, err := PreparePaymentConfig(req)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		values[SettingRechargeBonusContent] = string(encoded)
		return rechargeBonusPreparedChange(values, current), nil
	}
	values, err := PreparePaymentConfig(req)
	if err != nil {
		return settings.PreparedChange{}, err
	}
	return rechargeBonusPreparedChange(values, current), nil
}

// normalizedRechargeBonusFields copies fields currently owned by the activity presentation.
// Each configured FAQ contributes its two stable keys. Missing values become
// empty strings and deleted questions lose their submitted source and translation fields.
func normalizedRechargeBonusFields(fields map[string]string, activity RechargeBonusActivity) map[string]string {
	result := rechargeBonusSource(activity, "")
	for key := range result {
		result[key] = fields[key]
	}
	result["title"] = strings.TrimSpace(result["title"])
	result["subtitle"] = strings.TrimSpace(result["subtitle"])
	result["notice"] = strings.TrimSpace(result["notice"])
	return result
}

// validateRechargeBonusFields checks source and translated text against presentation limits.
// The same rune budgets govern administrator originals and submitted translations.
// Required fields are checked on the source schedule and reviewed translations,
// permitting stale translations to retain missing text after a source FAQ addition.
func validateRechargeBonusFields(fields map[string]string) error {
	for key, value := range fields {
		// Share the source editor's rune budgets with translated presentation fields.
		limit := 10000
		switch {
		case key == "title":
			limit = 160
		case key == "subtitle":
			limit = 500
		case strings.HasSuffix(key, ".question"):
			limit = 300
		case strings.HasSuffix(key, ".answer"):
			limit = 3000
		}
		if utf8.RuneCountInString(value) > limit {
			return apperror.BadRequest("INVALID_RECHARGE_BONUS", "Recharge presentation text exceeds its length limit.")
		}
	}
	return nil
}

// validateReviewedRechargeBonusFields checks completeness before approving translated presentation.
// Published activities require translated titles, and each active FAQ requires
// both parts. Incomplete translations remain editable until their reviewer supplies those fields.
func validateReviewedRechargeBonusFields(fields map[string]string, activity RechargeBonusActivity) error {
	if activity.PageEnabled && strings.TrimSpace(fields["title"]) == "" {
		return apperror.BadRequest("INVALID_RECHARGE_BONUS", "Published recharge activities require a title.")
	}
	for _, faq := range activity.FAQs {
		if strings.TrimSpace(fields["faq."+faq.ID+".question"]) == "" ||
			strings.TrimSpace(fields["faq."+faq.ID+".answer"]) == "" {
			return apperror.BadRequest("INVALID_RECHARGE_BONUS", "Activity questions and answers require text.")
		}
	}
	return nil
}

// rechargeBonusPreparedChange attaches raw comparison values to the payment batch.
// All participating records are expected even when a plain schedule update leaves
// content untouched, so a simultaneous localized edit can invalidate the entire save.
func rechargeBonusPreparedChange(values, current map[string]string) settings.PreparedChange {
	expected := map[string]*string{}
	for _, key := range []string{SettingRechargeBonusContent, SettingRechargeBonusActivity, SettingRechargeBonusNotice} {
		if raw, exists := current[key]; exists {
			expected[key] = &raw
		} else {
			expected[key] = nil
		}
	}
	return settings.PreparedChange{Module: "payment", Values: values, Expected: expected}
}
