package ticket

import (
	"path"
	"strings"
)

// attachment_preview.go revalidates private attachment bytes and selects their
// browser preview representation. Historical records can outlive upload-
// setting changes, so previews use fixed format limits after the caller has
// authorized ticket ownership and module access.
//
// Word files pass through document validation and bounded text extraction,
// returning text/plain for escaped display. Other files reuse upload
// validation, which normalizes images and checks PDF content. Stored MIME
// metadata must match the detected representation, with the defined WebP-to-
// PNG normalization accepted. The returned name, MIME, and bytes describe the
// actual preview while the original download remains separately available.

// PreviewTicketAttachment converts one authorized stored file into a validated
// preview representation. Callers check ticket access and current module
// availability before passing private bytes. The fixed twenty-MiB input cap is
// independent of administrator upload limits.
//
// Word MIME metadata is checked before extracting plain text. Images and PDFs
// reuse attachment normalization with format-level limits; the detected MIME
// must match stored metadata, apart from supported WebP conversion to PNG.
// This keeps historical files inspectable after upload limits change while
// rejecting inconsistent stored representations.
// @project-doc docs/domains/support_tickets.md#ticket_attachments
func PreviewTicketAttachment(file *TicketAttachment) (*TicketAttachmentUpload, error) {
	if file == nil || len(file.Data) == 0 || len(file.Data) > 20<<20 {
		return nil, ticketAttachmentInvalid("附件为空或超过预览大小限制")
	}
	name := sanitizeTicketFilename(file.Filename)
	if err := validateTicketFilename(name); err != nil {
		return nil, ticketAttachmentInvalid(err.Error())
	}
	ext := strings.ToLower(path.Ext(name))
	if ext == ".doc" || ext == ".docx" {
		expectedMIME := "application/msword"
		if ext == ".docx" {
			expectedMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		}
		if file.ContentType != expectedMIME {
			return nil, ticketAttachmentInvalid("附件格式与内容类型不一致")
		}
		text, err := PreviewTicketWord(file.Data, expectedMIME)
		if err != nil {
			return nil, err
		}
		// Return Word body content as text/plain so browser interpolation displays
		// document characters safely and does not execute document markup.
		return &TicketAttachmentUpload{Name: name + ".txt", ContentType: "text/plain; charset=utf-8", Data: []byte(text)}, nil
	}
	// Historical previews use fixed format limits so lowering upload limits does
	// not block access to files already attached to a conversation.
	files, err := ValidateTicketAttachments([]TicketAttachmentUpload{{Name: name, Data: file.Data}}, &TicketConfig{
		MaxAttachments: 1, MaxAttachmentSizeMB: 20,
	})
	if err != nil {
		return nil, err
	}
	preview := files[0]
	// WebP normalization produces PNG bytes and metadata; all other representations
	// must agree with their saved MIME before being sent inline.
	if preview.ContentType != file.ContentType && (ext != ".webp" || file.ContentType != "image/webp" || preview.ContentType != "image/png") {
		return nil, ticketAttachmentInvalid("附件格式与内容类型不一致")
	}
	return &preview, nil
}
