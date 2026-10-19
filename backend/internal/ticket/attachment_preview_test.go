package ticket

import (
	"bytes"
	"image"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// TestPreviewTicketAttachmentRevalidatesImagesAndPDF exercises stored images
// with trailing bytes and previously accepted PDF content. Preview normalization
// returns a trusted representation after applying the current format-level
// validation rules.
func TestPreviewTicketAttachmentRevalidatesImagesAndPDF(t *testing.T) {
	for _, format := range []string{"png", "jpeg", "gif"} {
		data := append(ticketTestImage(t, format), []byte("inert-old-payload")...)
		preview, err := PreviewTicketAttachment(&TicketAttachment{Filename: "image." + format, ContentType: "image/" + format, Data: data})
		require.NoError(t, err)
		require.Equal(t, "image/"+format, preview.ContentType)
		require.NotContains(t, string(preview.Data), "inert-old-payload")
		_, _, err = image.Decode(bytes.NewReader(preview.Data))
		require.NoError(t, err)
	}
	data := ticketTestPDF()
	preview, err := PreviewTicketAttachment(&TicketAttachment{Filename: "../../账单.pdf", ContentType: "application/pdf", Data: data})
	require.NoError(t, err)
	require.Equal(t, "账单.pdf", preview.Name)
	require.Equal(t, data, preview.Data)
}

// TestPreviewTicketAttachmentRejectsUnexpectedTypesAndSize exercises historical
// files with unsupported MIME values or excessive content. Preview rejects them
// under fixed format limits even when upload configuration is not consulted.
func TestPreviewTicketAttachmentRejectsUnexpectedTypesAndSize(t *testing.T) {
	for _, file := range []*TicketAttachment{
		nil,
		{Filename: "empty.pdf", ContentType: "application/pdf"},
		{Filename: "large.pdf", ContentType: "application/pdf", Data: make([]byte, (20<<20)+1)},
		{Filename: "image.svg", ContentType: "image/svg+xml", Data: []byte("<svg/>")},
		{Filename: "image.html.png", ContentType: "image/png", Data: ticketTestImage(t, "png")},
		{Filename: "image.png", ContentType: "text/html", Data: ticketTestImage(t, "png")},
		{Filename: "word.docx", ContentType: "application/pdf", Data: ticketTestPDF()},
	} {
		preview, err := PreviewTicketAttachment(file)
		require.Equal(t, "TICKET_ATTACHMENT_INVALID", apperror.Reason(err))
		require.Nil(t, preview)
	}
}
