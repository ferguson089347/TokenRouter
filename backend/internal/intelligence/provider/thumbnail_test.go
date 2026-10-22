package provider

import (
	"bytes"
	"context"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestThumbnailRendererCapturesAndBlocksNetwork renders active HTML containing
// a canvas and attempted network access. The saved PNG contains the scripted
// drawing, and the network fixture receives no request from the isolated page.
func TestThumbnailRendererCapturesAndBlocksNetwork(t *testing.T) {
	if os.Getenv("CHROMIUM_PATH") == "" {
		t.Skip("CHROMIUM_PATH is required for the real browser capture test")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	t.Cleanup(server.Close)
	document := `<!doctype html><html><body style="margin:0;background:white"><canvas id="art" width="1280" height="960"></canvas><script>const c=document.getElementById('art').getContext('2d');c.fillStyle='#00aa00';c.fillRect(0,0,1280,960);fetch('` + server.URL + `').catch(()=>{})</script><img src="` + server.URL + `/image"></body></html>`
	data, err := NewThumbnailRenderer().Render(context.Background(), document)
	require.NoError(t, err)
	image, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, 640, image.Bounds().Dx())
	require.Equal(t, 480, image.Bounds().Dy())
	r, g, b, _ := image.At(50, 50).RGBA()
	require.Zero(t, r)
	require.Greater(t, g, uint32(0))
	require.Zero(t, b)
	require.Zero(t, requests.Load())
}
