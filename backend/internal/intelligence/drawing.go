package intelligence

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/intelligence/remote"
)

// drawing.go coordinates gateway generation with remote HTML assessment for
// drawing checks. A configured model and optional prompt determine the local
// request; an empty prompt selects the pelican cycling animation. Completed
// artwork and generation measurements are saved before any assessment request,
// so a classifier failure still leaves the document available for preview.
//
// Recovery distinguishes chargeable generation from repeatable classification.
// An interrupted generation stays unknown, while a saved document can be
// submitted again under the same idempotency identity. Assessment attempts are
// persisted before transmission and bounded across restarts. Lease comparisons
// protect every transition, and cancellation uses a separate persistence budget
// to retain an observed document or accepted classifier identity.

// defaultDrawingPrompt supplies a self-contained animation task when an
// administrator leaves the drawing instruction empty in the saved configuration.
const defaultDrawingPrompt = "请生成可直接运行的单文件HTML，使用内联SVG绘制鹈鹕骑自行车的二维循环动画。画面以鹈鹕和自行车为主体，展示清晰的身体结构、踩踏动作和车轮转动，配合协调的背景、配色与层次。动画应流畅自然、衔接连续，并适配不同屏幕尺寸。禁止依赖外部资源，只输出完整HTML，不要代码围栏或解释文字。"

// generateDrawing requests one document after the caller persists generation
// intent. It saves the redacted HTML, actual prompt, and reported generation
// measurements before releasing the lease for assessment. Failed or ambiguous
// model calls terminate the observation and require a manual generation retry.
//
// Persistence outlives caller cancellation for a bounded interval. A failed save
// leaves submitting as the durable state, so recovery reports uncertainty.
// Successful saves publish generated for evaluation using the retained document.
func (s *IntelligenceService) generateDrawing(ctx context.Context, run *IntelligenceRun, c *IntelligenceConfig, secret string) {
	prompt := c.Prompt
	if prompt == "" {
		prompt = defaultDrawingPrompt
	}
	run.Question = redactIntelligenceText(prompt, secret, "")
	result, err := s.client.Generate(ctx, remote.CreateRequest{
		Benchmark:       "pelican",
		BaseURL:         c.BaseURL,
		APIKey:          secret,
		Model:           c.Model,
		Protocol:        c.Protocol,
		ReasoningEffort: c.ReasoningEffort,
		ServiceTier:     c.ServiceTier,
	}, prompt)
	if err != nil || result == nil || strings.TrimSpace(result.HTML) == "" || len(result.HTML) > remote.MaxHTMLBytes {
		message := "HTML生成失败或未返回完整文档，未提交评价；未自动重试，请核对使用记录"
		var httpErr *remote.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode >= 400 && httpErr.StatusCode <= 599 {
			message = fmt.Sprintf("HTML生成接口返回HTTP %d，未提交评价；未自动重试", httpErr.StatusCode)
			if httpErr.Diagnostic != nil {
				message = "HTML生成失败：" + httpErr.Diagnostic.SafeMessage() + "；未提交评价，未自动重试"
			}
		}
		s.finish(ctx, run, "error", "error", message)
		return
	}
	html := redactIntelligenceText(result.HTML, secret, "")
	if len(html) > remote.MaxHTMLBytes {
		s.finish(ctx, run, "error", "error", "HTML脱敏后超过2MiB限制，未保存作品或提交评价")
		return
	}
	run.HTML = html
	run.InputTokens = result.InputTokens
	run.OutputTokens = result.OutputTokens
	run.ReasoningTokens = result.ReasoningTokens
	run.DurationMS = result.DurationMS
	run.FirstTokenMS = result.FirstTokenMS
	run.HasArtifact = true
	run.HasDetail = true
	run.Status = "running"
	run.Phase = "generated"
	run.UpdatedAt = time.Now().UTC()
	// Save the observed artifact after generation cancellation so assessment recovery
	// can reuse it and users can inspect it before a classifier finishes.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	ok, err := s.repo.SaveRun(ctx, run, time.Now().UTC(), false)
	if err == nil && ok && s.thumbnails != nil {
		// Capture after publishing HTML so a failed browser cannot discard generated
		// artwork or prevent its assessment. Missing screenshots are retried by authorized reads.
		_, _ = s.Thumbnail(ctx, run)
	}
}

// assessDrawing submits retained artwork using credential-free HTML evaluation.
// It persists an attempt before transmission, then saves the accepted identity
// through applyResult. Transient failures and interrupted submissions can retry
// the saved document at most five times; generation is never part of this path.
//
// A rejected lease blocks the network request, preventing a replaced worker from
// submitting another evaluation. Every retry carries the same local run identity,
// and terminal assessment failures keep the generated HTML and measurements.
// @project-doc docs/domains/intelligence_tests.md#intelligence_generation
func (s *IntelligenceService) assessDrawing(ctx context.Context, run *IntelligenceRun) {
	if run.AssessmentAttempts >= 5 {
		s.finish(ctx, run, "unknown", "unknown", "HTML已生成，但评价多次未成功，可继续查看作品")
		return
	}
	run.AssessmentAttempts++
	run.Status = "submitting"
	run.Phase = "assessing"
	run.UpdatedAt = time.Now().UTC()
	// Record the attempt before contacting the classifier so restarts cannot reset
	// its retry budget or bypass the current worker's lease ownership check.
	ok, err := s.repo.SaveRun(ctx, run, time.Now().UTC(), true)
	if err != nil || !ok {
		return
	}
	// Match the gallery's run-and-document hash so retries and restarted workers
	// identify the same artwork while different saved documents receive distinct assessment keys.
	digest := sha256.Sum256([]byte(run.ID + "\x00" + run.HTML))
	idempotencyKey := fmt.Sprintf("html-gallery-%x", digest)
	result, err := s.client.Create(ctx, remote.CreateRequest{Benchmark: "html", HTML: run.HTML}, idempotencyKey)
	if err == nil && result != nil && result.ID != "" {
		run.RemoteID = result.ID
		s.applyResult(ctx, run, result, "")
		return
	}
	var httpErr *remote.HTTPError
	permanent := errors.As(err, &httpErr) && httpErr.StatusCode >= 400 && httpErr.StatusCode < 500 && httpErr.StatusCode != 429
	if permanent || run.AssessmentAttempts >= 5 {
		message := "HTML已生成，但评价提交失败，可继续查看作品"
		if httpErr != nil && httpErr.StatusCode >= 400 && httpErr.StatusCode <= 599 {
			message = fmt.Sprintf("HTML已生成，但评价接口返回HTTP %d，可继续查看作品", httpErr.StatusCode)
		}
		s.finish(ctx, run, "unknown", "unknown", message)
		return
	}
	delay := time.Duration(run.AssessmentAttempts) * 15 * time.Second
	if errors.As(err, &httpErr) && httpErr.RetryAfter > delay {
		delay = httpErr.RetryAfter
	}
	// Keep supplier retry hints inside the observation window so a long Retry-After
	// cannot leave this configuration occupied past its normal completion deadline.
	delay = min(delay, max(0, time.Until(run.CreatedAt.Add(30*time.Minute))))
	// Retain generated state after an uncertain classifier response. Reusing the
	// saved HTML and idempotency key allows retry without requesting another model output.
	run.Status = "running"
	run.Phase = "generated"
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	_, _ = s.repo.SaveRun(ctx, run, time.Now().Add(delay), false)
}

// redactIntelligenceText removes known credentials and polling identities from
// generated content. Recognizable historical Key patterns are also removed so
// the saved document remains safe when the configured credential changes later.
func redactIntelligenceText(text, secret, remoteID string) string {
	if secret != "" {
		text = strings.ReplaceAll(text, secret, "[已隐藏密钥]")
	}
	if remoteID != "" {
		text = strings.ReplaceAll(text, remoteID, "[已隐藏任务凭证]")
	}
	return intelligenceKeyPattern.ReplaceAllString(text, "[已隐藏密钥]")
}
