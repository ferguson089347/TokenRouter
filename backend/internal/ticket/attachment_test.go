package ticket

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

const (
	ticketTestContentTypes = `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`
	ticketTestDocument     = `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>工单附件</w:t></w:r></w:p></w:body></w:document>`
)

// ticketTestImage encodes a small valid image for attachment content-validation
// assertions.
func ticketTestImage(t *testing.T, format string) []byte {
	t.Helper()
	var out bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var err error
	switch format {
	case "png":
		err = png.Encode(&out, img)
	case "jpeg":
		err = jpeg.Encode(&out, img, nil)
	case "gif":
		err = gif.Encode(&out, img, nil)
	}
	require.NoError(t, err)
	return out.Bytes()
}

// ticketTestZIP builds an in-memory Office archive from named fixture parts.
func ticketTestZIP(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for name, data := range entries {
		entry, err := w.Create(name)
		require.NoError(t, err)
		_, err = entry.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return out.Bytes()
}

// ticketTestDOC builds a minimal binary Word compound document for format-
// validation tests.
func ticketTestDOC() []byte {
	const sectorSize = 512
	const free uint32 = 0xffffffff
	const end uint32 = 0xfffffffe
	data := make([]byte, 11*sectorSize)
	le := binary.LittleEndian
	copy(data, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1})
	le.PutUint16(data[26:28], 3)
	le.PutUint16(data[28:30], 0xfffe)
	le.PutUint16(data[30:32], 9)
	le.PutUint16(data[32:34], 6)
	le.PutUint32(data[44:48], 1)
	le.PutUint32(data[48:52], 1)
	le.PutUint32(data[56:60], 4096)
	le.PutUint32(data[60:64], end)
	le.PutUint32(data[68:72], end)
	for offset := 76; offset < 512; offset += 4 {
		le.PutUint32(data[offset:offset+4], free)
	}
	le.PutUint32(data[76:80], 0)
	fat := data[512:1024]
	for offset := 0; offset < sectorSize; offset += 4 {
		le.PutUint32(fat[offset:offset+4], free)
	}
	le.PutUint32(fat[0:4], 0xfffffffd)
	le.PutUint32(fat[4:8], end)
	for id := 2; id < 9; id++ {
		le.PutUint32(fat[id*4:(id+1)*4], uint32(id+1))
	}
	le.PutUint32(fat[36:40], end)
	entry := func(offset int, name string, kind byte, start uint32, size uint64, child uint32) {
		block := data[offset : offset+128]
		name16 := utf16.Encode([]rune(name))
		for i, r := range name16 {
			le.PutUint16(block[i*2:i*2+2], r)
		}
		le.PutUint16(block[64:66], uint16((len(name16)+1)*2))
		block[66], block[67] = kind, 1
		le.PutUint32(block[68:72], free)
		le.PutUint32(block[72:76], free)
		le.PutUint32(block[76:80], child)
		le.PutUint32(block[116:120], start)
		le.PutUint64(block[120:128], size)
	}
	entry(1024, "Root Entry", 5, end, 0, 1)
	entry(1152, "WordDocument", 2, 2, 4096, free)
	le.PutUint16(data[1536:1538], 0xa5ec)
	le.PutUint16(data[1538:1540], 0x00c1)
	return data
}

// TestTicketAttachmentValidFormatsAndCanonicalMIME exercises supported image,
// PDF, and Word fixtures with untrusted MIME declarations. Actual decoded
// formats determine the accepted content type and resulting normalized
// attachment metadata.
func TestTicketAttachmentValidFormatsAndCanonicalMIME(t *testing.T) {
	webp, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	require.NoError(t, err)
	tests := []struct {
		name, mime string
		data       []byte
	}{
		{"照片.JPG", "image/jpeg", ticketTestImage(t, "jpeg")},
		{"照片.jpeg", "image/jpeg", ticketTestImage(t, "jpeg")},
		{"截图.png", "image/png", ticketTestImage(t, "png")},
		{"动画.gif", "image/gif", ticketTestImage(t, "gif")},
		{"截图.webp", "image/png", webp},
		{"账单.pdf", "application/pdf", ticketTestPDF()},
		{"说明.doc", "application/msword", ticketTestDOC()},
		{"说明.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", ticketTestZIP(t, map[string][]byte{"[Content_Types].xml": []byte(ticketTestContentTypes), "word/document.xml": []byte(ticketTestDocument)})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := ValidateTicketAttachments([]TicketAttachmentUpload{{Name: tt.name, ContentType: "text/html", Data: tt.data}}, defaultTicketConfig())
			require.NoError(t, err)
			require.Len(t, files, 1)
			require.Equal(t, tt.mime, files[0].ContentType)
			if !strings.HasPrefix(tt.mime, "image/") {
				require.Equal(t, tt.data, files[0].Data)
			} else {
				_, _, err := image.Decode(bytes.NewReader(files[0].Data))
				require.NoError(t, err)
			}
		})
	}
}

// TestTicketAttachmentRejectsDisguisedContent exercises extensions paired with
// incompatible or active bytes. Validation rejects disguised documents and
// malformed containers rather than trusting the client-provided filename or MIME
// value.
func TestTicketAttachmentRejectsDisguisedContent(t *testing.T) {
	excel := ticketTestDOC()
	for i := 1152; i < 1216; i++ {
		excel[i] = 0
	}
	for i, r := range utf16.Encode([]rune("Workbook")) {
		binary.LittleEndian.PutUint16(excel[1152+i*2:1154+i*2], r)
	}
	binary.LittleEndian.PutUint16(excel[1216:1218], 18)
	docx := ticketTestZIP(t, map[string][]byte{"[Content_Types].xml": []byte(ticketTestContentTypes), "word/document.xml": []byte(ticketTestDocument)})
	tests := []struct {
		name string
		data []byte
	}{
		{"html.png", []byte("<html>伪装图片</html>")},
		{"png.jpg", ticketTestImage(t, "png")},
		{"pdf.doc", []byte("%PDF-1.7")},
		{"text.pdf", []byte("这是一个PDF")},
		{"excel.doc", excel},
		{"macro.docm", docx},
		{"script.svg", []byte(`<svg onload="alert(1)"/>`)},
		{"file.exe", []byte("MZ executable")},
		{"empty.png", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateTicketAttachments([]TicketAttachmentUpload{{Name: tt.name, Data: tt.data}}, defaultTicketConfig())
			require.Equal(t, "TICKET_ATTACHMENT_INVALID", apperror.Reason(err))
		})
	}
}

// TestTicketAttachmentLimits exercises disabled uploads and configured count,
// file, and aggregate limits. Submissions outside those limits fail before
// accepted attachment records can reach message persistence.
func TestTicketAttachmentLimits(t *testing.T) {
	cfg := defaultTicketConfig()
	files := make([]TicketAttachmentUpload, 6)
	_, err := ValidateTicketAttachments(files, cfg)
	require.Equal(t, "TICKET_ATTACHMENT_LIMIT", apperror.Reason(err))
	cfg.MaxAttachments = 0
	_, err = ValidateTicketAttachments(files[:1], cfg)
	require.Equal(t, "TICKET_ATTACHMENT_LIMIT", apperror.Reason(err))
	result, err := ValidateTicketAttachments(nil, cfg)
	require.NoError(t, err)
	require.Empty(t, result)
	cfg.MaxAttachments, cfg.MaxAttachmentSizeMB = 10, 1
	_, err = ValidateTicketAttachments([]TicketAttachmentUpload{{Name: "huge.pdf", Data: make([]byte, (1<<20)+1)}}, cfg)
	require.Equal(t, "TICKET_ATTACHMENT_TOO_LARGE", apperror.Reason(err))
	cfg.MaxAttachmentSizeMB = 20
	large := make([]byte, 20<<20)
	files = []TicketAttachmentUpload{{Name: "1.pdf", Data: large}, {Name: "2.pdf", Data: large}, {Name: "3.pdf", Data: large[:10<<20]}}
	_, err = ValidateTicketAttachments(files, cfg)
	// At exactly the aggregate limit, content validation still runs; fabricated
	// PDF bytes cannot be accepted solely because their total size is allowed.
	require.Equal(t, "TICKET_ATTACHMENT_INVALID", apperror.Reason(err))
	files[2].Data = large[:(10<<20)+1]
	_, err = ValidateTicketAttachments(files, cfg)
	require.Equal(t, "TICKET_ATTACHMENT_TOO_LARGE", apperror.Reason(err))
	_, err = ValidateTicketAttachments(nil, nil)
	require.Error(t, err)
	cfg.MaxAttachments = 100
	_, err = ValidateTicketAttachments(nil, cfg)
	require.Error(t, err)
}

// TestTicketAttachmentRejectsImageDimensionBomb exercises an image header
// declaring excessive dimensions. Dimension validation rejects it before full
// image decoding can allocate pixels based on the forged header.
func TestTicketAttachmentRejectsImageDimensionBomb(t *testing.T) {
	data := ticketTestImage(t, "png")
	binary.BigEndian.PutUint32(data[16:20], 20000)
	binary.BigEndian.PutUint32(data[20:24], 20000)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	_, err := ValidateTicketAttachments([]TicketAttachmentUpload{{Name: "huge.png", Data: data}}, defaultTicketConfig())
	require.Equal(t, "TICKET_ATTACHMENT_INVALID", apperror.Reason(err))
	require.Contains(t, apperror.Message(err), "图片尺寸过大")
}

// TestTicketAttachmentFilenameCleanup exercises path separators, controls, long
// Unicode names, and dangerous suffixes. Names remain bounded and valid UTF-8
// while misleading executable extensions cause upload rejection.
func TestTicketAttachmentFilenameCleanup(t *testing.T) {
	tests := []struct{ input, want string }{
		{`C:\fakepath\截图.png`, "截图.png"},
		{`../../secret/账单.pdf`, "账单.pdf"},
		{"../截\r\n图\x00.png", "截图.png"},
		{"报价\u202edoc.exe.pdf", "报价doc.exe.pdf"},
		{`  不合法:<文件>?.docx  `, "不合法文件.docx"},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, sanitizeTicketFilename(tt.input))
	}
	name := sanitizeTicketFilename(strings.Repeat("中", 100) + ".png")
	require.LessOrEqual(t, len(name), ticketMaxFilenameBytes)
	require.True(t, utf8.ValidString(name))
	require.True(t, strings.HasSuffix(name, ".png"))
	files, err := ValidateTicketAttachments([]TicketAttachmentUpload{{Name: `C:\fakepath\截图.png`, Data: ticketTestImage(t, "png")}}, defaultTicketConfig())
	require.NoError(t, err)
	require.Equal(t, "截图.png", files[0].Name)
}

// TestTicketAttachmentDocxStructuralLimits exercises DOCX archives with invalid
// paths, excessive expansion, or missing required parts. The validator rejects
// each malformed container before extracting its document body.
func TestTicketAttachmentDocxStructuralLimits(t *testing.T) {
	tests := []struct {
		name    string
		entries map[string][]byte
	}{
		{"缺少主文档", map[string][]byte{"[Content_Types].xml": []byte(ticketTestContentTypes)}},
		{"空ZIP", map[string][]byte{}},
		{"错误主类型", map[string][]byte{"[Content_Types].xml": []byte(strings.ReplaceAll(ticketTestContentTypes, "wordprocessingml.document", "spreadsheetml.sheet")), "word/document.xml": []byte(ticketTestDocument)}},
		{"宏伪装DOCX", map[string][]byte{"[Content_Types].xml": []byte(strings.ReplaceAll(ticketTestContentTypes, "wordprocessingml.document", "ms-word.document.macroEnabled")), "word/document.xml": []byte(ticketTestDocument)}},
		{"隐藏宏", map[string][]byte{"[Content_Types].xml": []byte(ticketTestContentTypes), "word/document.xml": []byte(ticketTestDocument), "word/vbaProject.bin": []byte("macro")}},
		{"路径穿越", map[string][]byte{"[Content_Types].xml": []byte(ticketTestContentTypes), "word/document.xml": []byte(ticketTestDocument), "../payload": []byte("data")}},
		{"错误XML根", map[string][]byte{"[Content_Types].xml": []byte(ticketTestContentTypes), "word/document.xml": []byte(`<html/>`)}},
		{"损坏XML", map[string][]byte{"[Content_Types].xml": []byte(ticketTestContentTypes), "word/document.xml": []byte(`<w:document><broken>`)}},
		{"多个XML根", map[string][]byte{"[Content_Types].xml": []byte(ticketTestContentTypes), "word/document.xml": []byte(ticketTestDocument + ticketTestDocument)}},
		{"XML嵌套炸弹", map[string][]byte{"[Content_Types].xml": []byte(ticketTestContentTypes), "word/document.xml": []byte(strings.Replace(ticketTestDocument, "<w:body>", "<w:body>"+strings.Repeat("<w:p>", 257)+strings.Repeat("</w:p>", 257), 1))}},
		{"编码隐藏宏", map[string][]byte{"[Content_Types].xml": []byte(strings.ReplaceAll(ticketTestContentTypes, "wordprocessingml.document", "ms-word.document.macro&#69;nabled")), "word/document.xml": []byte(ticketTestDocument)}},
		{"XML解压超限", map[string][]byte{"[Content_Types].xml": []byte(ticketTestContentTypes), "word/document.xml": bytes.Repeat([]byte("x"), ticketMaxDocxXMLBytes+1)}},
		{"总解压超限", map[string][]byte{"[Content_Types].xml": []byte(ticketTestContentTypes), "word/document.xml": []byte(ticketTestDocument), "word/media/bomb": bytes.Repeat([]byte("x"), ticketMaxDocxExpandedBytes+1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateTicketAttachments([]TicketAttachmentUpload{{Name: "file.docx", Data: ticketTestZIP(t, tt.entries)}}, defaultTicketConfig())
			require.Equal(t, "TICKET_ATTACHMENT_INVALID", apperror.Reason(err))
		})
	}
	entries := make(map[string][]byte, ticketMaxDocxEntries+1)
	for i := 0; i <= ticketMaxDocxEntries; i++ {
		entries[strings.Repeat("a", i+1)] = nil
	}
	require.Error(t, validateTicketDocx(ticketTestZIP(t, entries)))
	require.NoError(t, validateTicketDocx(ticketTestZIP(t, map[string][]byte{"[Content_Types].xml": []byte("\xef\xbb\xbf" + ticketTestContentTypes), "word/document.xml": []byte("\xef\xbb\xbf" + ticketTestDocument)})))
}

// TestTicketAttachmentDOCRejectsCorruptDirectory exercises binary Word
// allocation and directory corruption. Invalid chains, unsupported storages, and
// mismatched document headers are rejected before the attachment can be
// accepted.
func TestTicketAttachmentDOCRejectsCorruptDirectory(t *testing.T) {
	t.Run("目录链循环", func(t *testing.T) {
		data := ticketTestDOC()
		binary.LittleEndian.PutUint32(data[516:520], 1)
		require.Error(t, validateTicketDOC(data))
	})
	t.Run("目录树循环", func(t *testing.T) {
		data := ticketTestDOC()
		binary.LittleEndian.PutUint32(data[1092:1096], 0)
		binary.LittleEndian.PutUint32(data[1100:1104], 0)
		require.Error(t, validateTicketDOC(data))
	})
	t.Run("孤立Word目录", func(t *testing.T) {
		data := ticketTestDOC()
		binary.LittleEndian.PutUint32(data[1100:1104], 0xffffffff)
		require.Error(t, validateTicketDOC(data))
	})
	t.Run("伪造Word流", func(t *testing.T) {
		data := ticketTestDOC()
		copy(data[1536:1540], "xlsx")
		require.Error(t, validateTicketDOC(data))
	})
	t.Run("截断结构", func(t *testing.T) {
		require.Error(t, validateTicketDOC(ticketTestDOC()[:1025]))
	})
}

// TestTicketAttachmentImageRewritingRemovesPayloadAndRejectsTruncation exercises
// images carrying trailing payloads and truncated content. Reencoding retains
// valid pixels, removes extra bytes, and rejects files that cannot be completely
// decoded.
func TestTicketAttachmentImageRewritingRemovesPayloadAndRejectsTruncation(t *testing.T) {
	for _, format := range []string{"png", "jpeg", "gif"} {
		t.Run(format, func(t *testing.T) {
			original := ticketTestImage(t, format)
			payload := append(append([]byte(nil), original...), []byte("<?php invalid_fixture(); ?><script>inert-test</script>")...)
			files, err := ValidateTicketAttachments([]TicketAttachmentUpload{{Name: "image." + format, Data: payload}}, defaultTicketConfig())
			require.NoError(t, err)
			require.NotContains(t, string(files[0].Data), "inert-test")
			require.NotContains(t, string(files[0].Data), "<?php")
			again, err := ValidateTicketAttachments([]TicketAttachmentUpload{{Name: "image." + format, Data: payload}}, defaultTicketConfig())
			require.NoError(t, err)
			require.Equal(t, files, again, "相同原始上传重试必须产生相同幂等摘要")
			_, err = ValidateTicketAttachments([]TicketAttachmentUpload{{Name: "image." + format, Data: original[:len(original)/2]}}, defaultTicketConfig())
			require.Error(t, err)
		})
	}
}

// TestTicketAttachmentAnimatedGIFKeepsOnlyFirstFrame exercises an animated GIF
// containing multiple frames. The normalized attachment contains a valid single
// image, bounding retained animation data to the first decoded frame.
func TestTicketAttachmentAnimatedGIFKeepsOnlyFirstFrame(t *testing.T) {
	palette := color.Palette{color.Black, color.White}
	img := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
	var out bytes.Buffer
	require.NoError(t, gif.EncodeAll(&out, &gif.GIF{Image: []*image.Paletted{img, img}, Delay: []int{1, 1}}))
	files, err := ValidateTicketAttachments([]TicketAttachmentUpload{{Name: "animation.gif", Data: out.Bytes()}}, defaultTicketConfig())
	require.NoError(t, err)
	decoded, err := gif.DecodeAll(bytes.NewReader(files[0].Data))
	require.NoError(t, err)
	require.Len(t, decoded.Image, 1)
}

// TestTicketAttachmentDangerousDoubleExtensions exercises filenames containing
// executable suffixes before a supported final extension. Upload validation
// rejects those names so downstream downloads cannot disguise script or
// executable content.
func TestTicketAttachmentDangerousDoubleExtensions(t *testing.T) {
	for _, name := range []string{"payload.php.png", "payload.PS1.pdf", "document.docm.docx", "image.png.exe", "payload.js.jpg"} {
		_, err := ValidateTicketAttachments([]TicketAttachmentUpload{{Name: name, ContentType: "image/png", Data: ticketTestImage(t, "png")}}, defaultTicketConfig())
		require.Equal(t, "TICKET_ATTACHMENT_INVALID", apperror.Reason(err), name)
	}
}

// TestTicketAttachmentImageOutputAndConcurrencyBounds exercises a bounded image
// output buffer and occupied decoder slots. Oversized reencoding fails, while
// processor exhaustion returns the retryable busy reason to callers.
func TestTicketAttachmentImageOutputAndConcurrencyBounds(t *testing.T) {
	_, _, err := normalizeTicketImage(ticketTestImage(t, "png"), ".png", 10)
	require.Error(t, err)
	for range cap(ticketImageSlots) {
		ticketImageSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for range cap(ticketImageSlots) {
			<-ticketImageSlots
		}
	})
	_, _, err = normalizeTicketImage(ticketTestImage(t, "png"), ".png", 1<<20)
	require.ErrorIs(t, err, ErrTicketAttachmentBusy)
	_, err = ValidateTicketAttachments([]TicketAttachmentUpload{{Name: "image.png", Data: ticketTestImage(t, "png")}}, defaultTicketConfig())
	require.Equal(t, "TICKET_ATTACHMENT_BUSY", apperror.Reason(err))
	require.Equal(t, 503, int(apperror.CategoryOf(err)))
}

// TestTicketAttachmentDocxRejectsActivePartsAndXML exercises Office
// relationships, fields, and active parts across the complete archive. Dangerous
// content is rejected while ordinary web hyperlinks remain valid document text.
func TestTicketAttachmentDocxRejectsActivePartsAndXML(t *testing.T) {
	relationship := func(kind, mode, target string) []byte {
		return []byte(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/` + kind + `" TargetMode="` + mode + `" Target="` + target + `"/></Relationships>`)
	}
	tests := []struct {
		name, entry string
		data        []byte
	}{
		{"DTD不定义实体也拒绝", "word/settings.xml", []byte(`<!DOCTYPE settings><settings/>`)},
		{"外部实体", "word/settings.xml", []byte(`<!DOCTYPE settings [<!ENTITY test SYSTEM "file:///fixture-never-read">]><settings>&test;</settings>`)},
		{"处理指令", "word/settings.xml", []byte(`<?xml-stylesheet href="https://example.invalid/payload"?><settings/>`)},
		{"OLE对象", "word/document.xml", []byte(strings.Replace(ticketTestDocument, "<w:body>", "<w:body><w:object/>", 1))},
		{"嵌入压缩包", "word/embeddings/oleObject1.bin", []byte("inert")},
		{"ActiveX", "word/activeX/activeX1.xml", []byte("<control/>")},
		{"嵌入脚本", "word/script.js", []byte("inert")},
		{"外部模板", "word/_rels/settings.xml.rels", relationship("attachedTemplate", "External", "https://example.invalid/template")},
		{"外部图片", "word/_rels/document.xml.rels", relationship("image", "External", "https://example.invalid/image.png")},
		{"脚本链接", "word/_rels/document.xml.rels", relationship("hyperlink", "External", "javascript:inert")},
		{"DDE域", "word/document.xml", []byte(strings.Replace(ticketTestDocument, "<w:body>", `<w:body><w:fldSimple w:instr="D&#68;EAUTO fixture"/>`, 1))},
		{"拆分DDE域", "word/document.xml", []byte(strings.Replace(ticketTestDocument, "<w:body>", `<w:body><w:instrText>D</w:instrText><w:instrText>DEAUTO fixture</w:instrText>`, 1))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := map[string][]byte{"[Content_Types].xml": []byte(ticketTestContentTypes), "word/document.xml": []byte(ticketTestDocument)}
			entries[tt.entry] = tt.data
			_, err := ValidateTicketAttachments([]TicketAttachmentUpload{{Name: "file.docx", Data: ticketTestZIP(t, entries)}}, defaultTicketConfig())
			require.Equal(t, "TICKET_ATTACHMENT_INVALID", apperror.Reason(err))
		})
	}
	// An ordinary hyperlink references navigation text rather than a resource the
	// server loads, so the document remains acceptable for private preview.
	entries := map[string][]byte{"[Content_Types].xml": []byte(ticketTestContentTypes), "word/document.xml": []byte(ticketTestDocument), "word/_rels/document.xml.rels": relationship("hyperlink", "External", "https://example.com/help")}
	require.NoError(t, validateTicketDocx(ticketTestZIP(t, entries)))
	require.False(t, unsafeTicketWordField(`HYPERLINK "https://example.com/added"`), "普通超链接不能被 LINK 或 DDE 子串误伤")
	_, err := ValidateTicketAttachments([]TicketAttachmentUpload{{Name: "file.docx", Data: append(ticketTestZIP(t, entries), []byte("inert trailing payload")...)}}, defaultTicketConfig())
	require.Error(t, err)
}

// TestTicketAttachmentDOCRejectsMacroStorageAfterWordStream exercises a valid
// Word body followed by unsafe directory entries. The validator scans the
// complete directory and rejects active storage after finding the document
// stream.
func TestTicketAttachmentDOCRejectsMacroStorageAfterWordStream(t *testing.T) {
	for _, name := range []string{"Macros", "ObjectPool", "VBA", "ActiveX"} {
		data := ticketTestDOC()
		// Place the unsafe storage after WordDocument so finding a valid body cannot
		// stop validation before inspecting later sibling directory entries.
		entry := data[1280:1408]
		entry[66] = 1
		chars := utf16.Encode([]rune(name))
		for i, value := range chars {
			binary.LittleEndian.PutUint16(entry[i*2:i*2+2], value)
		}
		binary.LittleEndian.PutUint16(entry[64:66], uint16(len(chars)*2+2))
		require.Error(t, validateTicketDOC(data), name)
	}
	data := ticketTestDOC()
	binary.LittleEndian.PutUint16(data[1546:1548], 0x0100)
	require.ErrorContains(t, validateTicketDOC(data), "加密")
	data = ticketTestDOC()
	binary.LittleEndian.PutUint32(data[548:552], 2)
	require.Error(t, validateTicketDOC(data), "拒绝 WordDocument 流链循环")
}
