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
// Recovery bounds chargeable generation and repeatable classification separately.
// Generation failures can requeue the same task at most twice, with each attempt
// recorded before transmission. An interrupted submission stays unknown, while a
// saved document can be submitted again under the same assessment identity.
// Lease comparisons protect every transition, and cancellation uses a separate
// persistence budget to retain an observed document or accepted classifier identity.

const (
	// defaultDrawingPrompt supplies a self-contained animation task when an
	// administrator leaves the drawing instruction empty in the saved configuration.
	defaultDrawingPrompt = "请生成可直接运行的单文件HTML，使用内联SVG绘制鹈鹕骑自行车的二维循环动画。画面以鹈鹕和自行车为主体，展示清晰的身体结构、踩踏动作和车轮转动，配合协调的背景、配色与层次。动画应流畅自然、衔接连续，并适配不同屏幕尺寸。禁止依赖外部资源，只输出完整HTML，不要代码围栏或解释文字。"

	// drawingGenerationMaxAttempts bounds chargeable model submissions for one task,
	// including its initial request, across worker claims and service restarts.
	drawingGenerationMaxAttempts = 3
)

// generateDrawing requests one document after the caller persists generation
// intent. It saves the redacted HTML, actual prompt, and reported generation
// measurements before releasing the lease for assessment. Transient generation
// failures can requeue within the persisted attempt and observation budgets;
// permanent failures terminate the task for diagnostic reads.
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
		if s.retryDrawingGeneration(ctx, run, err) {
			return
		}
		message := "HTML生成失败或未返回完整文档，本次检测结束，请核对使用记录"
		if httpErr, ok := errors.AsType[*remote.HTTPError](err); ok {
			if httpErr.StatusCode >= 400 && httpErr.StatusCode <= 599 {
				message = fmt.Sprintf("HTML生成接口返回HTTP %d，本次检测结束", httpErr.StatusCode)
			}
			if httpErr.Diagnostic != nil {
				message = "HTML生成失败：" + httpErr.Diagnostic.SafeMessage() + "；本次检测结束"
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

// retryDrawingGeneration requeues transient generation failure within one
// durable task and reports whether it consumed failure handling. The caller
// persists GenerationAttempts before contacting the gateway. Permanent request,
// credential, permission, and quota errors terminate without another submission.
// Cancellation stops retries, and at most three gateway requests fit within
// the task's existing thirty-minute observation window.
//
// Requeueing releases the current lease and stores a delayed next claim while
// retaining the task identity and attempt count. A rejected or failed save leaves
// the durable submitting state for recovery; this caller then avoids a second
// competing terminal write. Each retry can incur another gateway usage charge.
func (s *IntelligenceService) retryDrawingGeneration(ctx context.Context, run *IntelligenceRun, failure error) bool {
	if ctx.Err() != nil || run.GenerationAttempts >= drawingGenerationMaxAttempts || !drawingGenerationRetryable(failure) {
		return false
	}
	now := time.Now().UTC()
	// Reuse the worker's observation limit so a delayed retry cannot keep one
	// configuration occupied indefinitely or restart an already expired detection round.
	remaining := run.CreatedAt.Add(30 * time.Minute).Sub(now)
	if remaining <= 0 {
		return false
	}
	// Increasing pauses separate repeated provider failures while the persisted
	// task keeps one history identity across all of its gateway generation attempts.
	delay := time.Duration(max(1, run.GenerationAttempts)) * 15 * time.Second
	var httpErr *remote.HTTPError
	if errors.As(failure, &httpErr) && httpErr.RetryAfter > delay {
		delay = httpErr.RetryAfter
	}
	if delay >= remaining {
		return false
	}
	run.Status = "queued"
	run.Phase = "retrying"
	run.Verdict = "pending"
	run.ErrorMessage = ""
	run.FinishedAt = nil
	run.UpdatedAt = now
	// Store retry intent separately from the failed exchange's cancellation so
	// the next worker claims the existing task and its cumulative attempt budget.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	_, _ = s.repo.SaveRun(ctx, run, now.Add(delay), false)
	return true
}

// drawingGenerationRetryable identifies transient gateway failures and missing
// complete output. Classified credential, quota, permission, and request failures
// stop retries even when a wrapper labels their HTTP response as a server error.
func drawingGenerationRetryable(failure error) bool {
	if failure == nil || errors.Is(failure, remote.ErrInvalidResponse) {
		return true
	}
	if httpErr, ok := errors.AsType[*remote.HTTPError](failure); ok {
		if httpErr.StatusCode >= 400 && httpErr.StatusCode < 500 && httpErr.StatusCode != 408 && httpErr.StatusCode != 429 {
			return false
		}
		if httpErr.Diagnostic != nil {
			switch httpErr.Diagnostic.Code {
			case "auth", "permission", "quota", "invalid_request", "model":
				return false
			case "timeout", "transport", "rate_limit", "upstream":
				return true
			}
		}
		return httpErr.StatusCode == 408 || httpErr.StatusCode == 429 || httpErr.StatusCode >= 500 && httpErr.StatusCode <= 599
	}
	var transport *remote.TransportError
	return errors.As(failure, &transport) && !transport.Canceled
}

// retryDrawingAssessment reschedules a failed or unknown evaluation from its
// retained document and reports whether it handled the terminal observation.
// Callers validate remote identity before invoking it and pass the classified
// remote failure when available. Permanent gateway-style failures, five spent
// submissions, and the task's observation deadline end evaluation retries.
//
// Clearing the polling identity selects assessment creation on the next claim.
// Generated HTML, generation counts, and the task identity remain available, so
// this path cannot submit another model generation. The same document-derived
// idempotency identity and cumulative assessment counter still bound repeated work.
func (s *IntelligenceService) retryDrawingAssessment(ctx context.Context, run *IntelligenceRun, failure *remote.RemoteError) bool {
	if !run.LocalGeneration || run.HTML == "" || run.AssessmentAttempts >= 5 || s.ctx.Err() != nil {
		return false
	}
	if failure != nil && !drawingGenerationRetryable(&remote.HTTPError{StatusCode: failure.HTTPStatus, Diagnostic: failure}) {
		return false
	}
	now := time.Now().UTC()
	// Use the same bounded assessment pause and observation deadline as submission
	// failures, retaining the generated document through every evaluation-only retry.
	delay := time.Duration(max(1, run.AssessmentAttempts)) * 15 * time.Second
	if !now.Add(delay).Before(run.CreatedAt.Add(30 * time.Minute)) {
		return false
	}
	run.RemoteID = ""
	run.Status = "running"
	run.Phase = "generated"
	run.Verdict = "pending"
	run.ErrorMessage = ""
	run.FinishedAt = nil
	run.UpdatedAt = now
	// Release the current claim with its reusable artwork before another worker
	// is allowed to retry evaluation under the same persisted submission budget.
	_, _ = s.repo.SaveRun(ctx, run, now.Add(delay), false)
	return true
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
