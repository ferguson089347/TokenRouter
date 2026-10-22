package provider

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/chromedp"
)

// thumbnail.go captures retained drawing HTML in a disposable headless browser.
// A synthetic document request receives the drawing and a restrictive content
// policy; every other request is rejected before transmission. The browser
// receives an isolated profile and a minimal environment, keeping application
// credentials out of its process. Screenshots use a fixed desktop viewport so
// historical thumbnails show the same composition regardless of the viewer's device.
//
// Each render has a bounded lifetime and owns its Chromium process. Cancellation
// closes the target and allocator, releasing the temporary profile. Request
// interception runs outside the browser event callback because protocol replies
// share that event loop. Container execution disables Chromium's namespace sandbox
// to support no-new-privileges deployments; the document's CSP sandbox and blocked
// network requests confine model scripts to their captured document.

// thumbnailOrigin names the one synthetic navigation fulfilled from saved HTML;
// it is never resolved or fetched over the network during screenshot rendering.
const thumbnailOrigin = "http://intelligence-preview.invalid/"

// ThumbnailRenderer captures static PNGs using a locally installed Chromium executable.
type ThumbnailRenderer struct{}

// NewThumbnailRenderer constructs a renderer whose browser starts for each capture.
// Chromium is discovered by chromedp unless CHROMIUM_PATH supplies an executable;
// missing browser installations produce a rendering error when a screenshot is requested.
func NewThumbnailRenderer() *ThumbnailRenderer { return &ThumbnailRenderer{} }

// Render captures one PNG after the drawing has run for seven hundred milliseconds.
// The viewport is 1280 by 960 CSS pixels with half-scale output, and the complete
// operation has a twenty-second timeout. Caller cancellation terminates the browser.
//
// The first synthetic document request receives the supplied HTML under a CSP
// sandbox. Every subsequent request, including navigations, is rejected by protocol
// interception. Event replies run asynchronously to avoid blocking the protocol
// event loop, and browser cancellation also cancels those pending replies. The
// subprocess receives no inherited application environment or saved browser profile.
func (*ThumbnailRenderer) Render(ctx context.Context, html string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	options := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	options = append(options, chromedp.NoSandbox, chromedp.DisableGPU,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.ModifyCmdFunc(func(cmd *exec.Cmd) {
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/tmp", "LANG=en_US.UTF-8"}
		}))
	if path := os.Getenv("CHROMIUM_PATH"); path != "" {
		options = append(options, chromedp.ExecPath(path))
	}
	allocator, release := chromedp.NewExecAllocator(ctx, options...)
	defer release()
	browser, closeBrowser := chromedp.NewContext(allocator)
	defer closeBrowser()
	var documentSent atomic.Bool
	chromedp.ListenTarget(browser, func(event any) {
		request, ok := event.(*fetch.EventRequestPaused)
		if !ok {
			return
		}
		// Reply outside the event callback because synchronous protocol calls would
		// prevent that same callback loop from receiving their completion messages.
		go func() {
			executor := cdp.WithExecutor(browser, chromedp.FromContext(browser).Target)
			if request.Request.URL == thumbnailOrigin && documentSent.CompareAndSwap(false, true) {
				policy := "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; connect-src 'none'; frame-src 'none'; worker-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'"
				// Cancellation or a replaced navigation can reject a reply; the parent
				// capture then fails or times out, and no request is allowed through.
				_ = fetch.FulfillRequest(request.RequestID, 200).
					WithResponseHeaders([]*fetch.HeaderEntry{{Name: "Content-Type", Value: "text/html; charset=utf-8"}, {Name: "Content-Security-Policy", Value: policy}}).
					WithBody(base64.StdEncoding.EncodeToString([]byte(html))).Do(executor)
				return
			}
			// Block every resource and navigation after the initial document so model
			// content cannot contact network services or read files from the rendering host.
			// If cancellation rejects this reply, the paused request stays blocked until
			// the browser process shuts down and releases its protocol session.
			_ = fetch.FailRequest(request.RequestID, "BlockedByClient").Do(executor)
		}()
	})
	var png []byte
	if err := chromedp.Run(browser,
		fetch.Enable(),
		chromedp.EmulateViewport(1280, 960, chromedp.EmulateScale(0.5)),
		chromedp.Navigate(thumbnailOrigin),
		chromedp.Sleep(700*time.Millisecond),
		chromedp.CaptureScreenshot(&png),
	); err != nil {
		return nil, errors.New("drawing screenshot failed")
	}
	// Keep PNG output inside the retained-artifact payload budget.
	if len(png) == 0 || len(png) > 2<<20 {
		return nil, errors.New("drawing screenshot exceeds size limit")
	}
	return png, nil
}
