package ticket

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// attachment_pdf.go checks PDF structure and active-content rules before a
// ticket accepts or previews the document. One processor slot limits
// concurrent parser work. Header and trailer checks, a read budget, parser
// resource limits, and a five-second context bound work on malformed or highly
// compressed inputs.
//
// Compressed object streams consume a cumulative expansion budget before lazy
// objects are decoded. The checker then examines dictionary actions and links
// across current objects and validates the page tree separately. Indirect
// references have bounded resolution and generation checks; page cycles and
// inconsistent declared counts are rejected. The parser runs offline and the
// server returns accepted bytes for local browser rendering.

// PDF limits cap object traversal, decoded stream bytes, and repeated input reads before private document acceptance or browser preview.
const (
	ticketPDFMaxObjects      = 10000
	ticketPDFMaxDepth        = 64
	ticketPDFMaxDecodedBytes = 8 << 20
	ticketPDFMaxTotalDecoded = 32 << 20
	ticketPDFMaxReadBytes    = 64 << 20
)

// ticketPDFSlots bounds document parser concurrency independently of image
// decoding, keeping simultaneous compressed-file inspection within one PDF
// allocation budget.
var ticketPDFSlots = make(chan struct{}, 1)

// ticketPDFReader enforces cancellation and a cumulative allowance across
// parser rescans.
type ticketPDFReader struct {
	*bytes.Reader                 // seekable source PDF bytes
	ctx           context.Context // worker cancellation context
	remaining     int64           // remaining cumulative byte allowance
}

// ticketPDFCheck tracks parser context, object visits, and remaining document
// expansion allowance.
type ticketPDFCheck struct {
	ctx       context.Context // worker cancellation context
	pdf       *model.Context  // parsed PDF object context
	remaining int64           // remaining cumulative byte allowance
	visited   int             // visited direct object count
}

// validateTicketPDF validates one complete unencrypted PDF under bounded
// parser resources. A busy processor returns ErrTicketAttachmentBusy, allowing
// callers to retain their upload draft. Parser panics become invalid-file
// errors for this request.
//
// Traditional cross-reference rows are counted before parsing, then compressed
// object streams consume the shared decoded-byte budget before lazy decoding.
// The checker examines all current objects for active actions and dangerous
// links. Finally, the catalog and page tree must resolve with valid counts.
// This order bounds allocation and inspection while supporting ordinary
// compressed-object documents.
// @project-doc docs/domains/support_tickets.md#ticket_attachments
func validateTicketPDF(data []byte) (err error) {
	if len(data) < 20 || len(data) > 20<<20 || !bytes.HasPrefix(data, []byte("%PDF-")) || !bytes.HasSuffix(bytes.TrimSpace(data), []byte("%%EOF")) {
		return fmt.Errorf("PDF 文件结构无效或包含尾随内容")
	}
	select {
	case ticketPDFSlots <- struct{}{}:
		defer func() { <-ticketPDFSlots }()
	default:
		return ErrTicketAttachmentBusy
	}
	// A parser panic rejects this uploaded document and releases its processing
	// slot, allowing other requests to continue handling valid files.
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("PDF 文件结构损坏，无法安全检查")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ticketPDFXRefBudget(data); err != nil {
		return err
	}
	conf := &model.Configuration{
		Reader15: true, ValidationMode: model.ValidationStrict, Offline: true,
		Cmd: model.VALIDATE,
		Limits: model.ResourceLimits{
			MaxStreamBytes: 20 << 20, MaxDecodeBytes: ticketPDFMaxDecodedBytes,
			MaxImagePixels: ticketMaxImagePixels, MaxImageBytes: 64 << 20,
			MaxObjectCount: ticketPDFMaxObjects, MaxXRefEntries: ticketPDFMaxObjects,
			MaxObjectStreamCount: 1000, MaxObjectStreamFirst: 64 << 10,
			MaxRecursionDepth: ticketPDFMaxDepth,
		},
	}
	reader := &ticketPDFReader{Reader: bytes.NewReader(data), ctx: ctx, remaining: ticketPDFMaxReadBytes}
	pdf, readErr := pdfcpu.ReadWithContext(ctx, reader, conf)
	if readErr != nil {
		return fmt.Errorf("PDF 文件损坏、已加密或超过安全解析限制")
	}
	if pdf.Encrypt != nil || pdf.E != nil {
		return fmt.Errorf("加密 PDF 无法安全检查，请上传未加密文档")
	}
	if len(pdf.Table) > ticketPDFMaxObjects || pdf.Root == nil {
		return fmt.Errorf("PDF 缺少文档结构或包含过多对象")
	}
	check := ticketPDFCheck{ctx: ctx, pdf: pdf, remaining: ticketPDFMaxTotalDecoded}
	// Charge decoded object-stream bytes before resolving lazy members, because
	// later object access can otherwise hide expansion outside the aggregate
	// budget.
	for _, entry := range pdf.Table {
		if entry.Free {
			continue
		}
		if objectStream, ok := entry.Object.(types.ObjectStreamDict); ok {
			limit := min(check.remaining, int64(ticketPDFMaxDecodedBytes))
			if limit <= 0 || objectStream.DecodeWithLimit(limit) != nil || int64(len(objectStream.Content)) > limit {
				return fmt.Errorf("PDF 对象流解压大小超过安全限制")
			}
			check.remaining -= int64(len(objectStream.Content))
		}
	}
	for _, entry := range pdf.Table {
		if entry.Free {
			continue
		}
		if lazy, ok := entry.Object.(types.LazyObjectStreamObject); ok {
			entry.Object, err = lazy.DecodedObject(ctx)
			if err != nil {
				return fmt.Errorf("PDF 压缩对象无效或超过安全限制")
			}
		}
	}
	for _, entry := range pdf.Table {
		if entry.Free {
			continue
		}
		if err = check.object(entry.Object, 0); err != nil {
			return err
		}
	}
	root, err := check.dict(*pdf.Root)
	if err != nil || root.NameEntry("Type") == nil || *root.NameEntry("Type") != "Catalog" {
		return fmt.Errorf("PDF 缺少有效文档目录")
	}
	pages, err := check.pages(root["Pages"], 0, make(map[int]bool))
	if err != nil || pages == 0 {
		return fmt.Errorf("PDF 页面结构无效")
	}
	return nil
}

// ticketPDFXRefBudget counts traditional cross-reference entries before the
// parser allocates them. It accepts PDF newline variants and rejects excessive
// entries or oversized lines; binary cross-reference allocations use the
// parser limits.
func ticketPDFXRefBudget(data []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 20<<20)
	scanner.Split(ticketPDFScanLines)
	count := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) < 5 || (line[len(line)-1] != 'n' && line[len(line)-1] != 'f') {
			continue
		}
		fields := bytes.Fields(line)
		if len(fields) != 3 || len(fields[2]) != 1 {
			continue
		}
		if _, err := strconv.ParseInt(string(fields[0]), 10, 64); err != nil {
			continue
		}
		if _, err := strconv.ParseInt(string(fields[1]), 10, 64); err != nil {
			continue
		}
		count++
		if count > ticketPDFMaxObjects {
			return fmt.Errorf("PDF 交叉引用条目超过安全限制")
		}
	}
	if scanner.Err() != nil {
		return fmt.Errorf("PDF 文档行长度超过安全限制")
	}
	return nil
}

// ticketPDFScanLines splits PDF lines using CR, LF, or CRLF endings. It
// returns the final unterminated line at EOF so row-budget counting sees the
// same records as the document parser.
func ticketPDFScanLines(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		advance := i + 1
		if data[i] == '\r' && advance < len(data) && data[advance] == '\n' {
			advance++
		}
		return advance, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// Read consumes PDF input while checking cancellation and cumulative read
// allowance. Repeated seeks do not replenish the allowance, so rescans of
// malformed cross-references cannot read indefinitely through this wrapper.
func (r *ticketPDFReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.remaining <= 0 {
		return 0, fmt.Errorf("PDF 读取预算已用尽")
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.Reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

// Seek checks cancellation before delegating a valid seek origin to the byte
// reader. Read allowance is retained across seeks, allowing the parser to
// revisit data within one cumulative budget.
func (r *ticketPDFReader) Seek(offset int64, whence int) (int64, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if whence != io.SeekStart && whence != io.SeekCurrent && whence != io.SeekEnd {
		return 0, fmt.Errorf("PDF 读取位置无效")
	}
	return r.Reader.Seek(offset, whence)
}

// resolve follows indirect object references within the configured depth
// limit. Missing objects, free entries, cycles, and mismatched generations are
// rejected before callers inspect the resolved value in the current table.
func (v *ticketPDFCheck) resolve(object types.Object) (types.Object, error) {
	seen := make(map[int]bool)
	for range ticketPDFMaxDepth {
		ref, ok := object.(types.IndirectRef)
		if !ok {
			return object, nil
		}
		id := int(ref.ObjectNumber)
		entry, exists := v.pdf.Table[id]
		if !exists || entry.Free || seen[id] || entry.Generation == nil || *entry.Generation != int(ref.GenerationNumber) {
			return nil, fmt.Errorf("PDF 对象引用无效")
		}
		seen[id] = true
		object = entry.Object
	}
	return nil, fmt.Errorf("PDF 对象引用层级过深")
}

// dict resolves an object and requires the resulting value to be a dictionary.
// Reference errors and incompatible object types remain validation errors for
// catalog, page, and action readers.
func (v *ticketPDFCheck) dict(object types.Object) (types.Dict, error) {
	object, err := v.resolve(object)
	if err != nil {
		return nil, err
	}
	dict, ok := object.(types.Dict)
	if !ok {
		return nil, fmt.Errorf("PDF 字典结构无效")
	}
	return dict, nil
}

// object recursively checks direct dictionary and array content for unsafe
// actions and links. Total visits, depth, and context cancellation bound the
// walk; indirect references are separately resolved when inspecting action
// identifiers.
func (v *ticketPDFCheck) object(object types.Object, depth int) error {
	v.visited++
	if depth > ticketPDFMaxDepth || v.visited > 200000 || v.ctx.Err() != nil {
		return fmt.Errorf("PDF 结构复杂度超过安全限制")
	}
	switch value := object.(type) {
	case types.Dict:
		for key, item := range value {
			switch key {
			case "JS", "JavaScript", "OpenAction", "AA", "Launch", "EmbeddedFiles", "EmbeddedFile", "EF", "AF", "RichMedia", "RichMediaContent", "RichMediaSettings", "Movie", "Sound", "XFA", "Collection", "Requirements":
				return fmt.Errorf("PDF 不能包含脚本、自动动作、嵌入文件或交互媒体")
			}
			if key == "S" || key == "Type" || key == "Subtype" {
				resolved, err := v.resolve(item)
				if err != nil {
					return err
				}
				if name, ok := resolved.(types.Name); ok {
					switch string(name) {
					case "JavaScript", "Launch", "SubmitForm", "ImportData", "GoToR", "GoToE", "Rendition", "Movie", "Sound", "RichMedia", "RichMediaExecute", "EmbeddedFile", "FileAttachment", "3D":
						return fmt.Errorf("PDF 包含不允许的文档动作或嵌入内容")
					}
				}
			}
			if key == "URI" {
				resolved, err := v.resolve(item)
				if err != nil {
					return err
				}
				text, err := model.Text(resolved)
				if err != nil {
					return fmt.Errorf("PDF 链接结构无效")
				}
				link, err := url.Parse(strings.TrimSpace(text))
				if err != nil || (link.Scheme != "http" && link.Scheme != "https" && link.Scheme != "mailto") {
					return fmt.Errorf("PDF 链接不能调用脚本、本地文件或外部程序")
				}
			}
			if err := v.object(item, depth+1); err != nil {
				return err
			}
		}
	case types.Array:
		for _, item := range value {
			if err := v.object(item, depth+1); err != nil {
				return err
			}
		}
	case types.StreamDict:
		return v.object(value.Dict, depth+1)
	case types.ObjectStreamDict:
		return v.object(value.Dict, depth+1)
	case types.XRefStreamDict:
		return v.object(value.Dict, depth+1)
	}
	return nil
}

// pages validates a page subtree and returns its actual leaf count. The shared
// seen set covers the complete tree, rejecting cycles and duplicate page
// references. Every node must resolve to a Page or Pages dictionary.
//
// Internal nodes require a nonempty Kids array. Child counts are accumulated
// within the object limit, then compared with the declared Count field.
// Context cancellation and depth bounds stop malformed trees before further
// traversal. Page contents, fonts, and images are left to the browser renderer
// after document safety checks succeed.
func (v *ticketPDFCheck) pages(object types.Object, depth int, seen map[int]bool) (int, error) {
	if depth > ticketPDFMaxDepth || v.ctx.Err() != nil {
		return 0, fmt.Errorf("PDF 页树过深")
	}
	if ref, ok := object.(types.IndirectRef); ok {
		id := int(ref.ObjectNumber)
		if seen[id] {
			return 0, fmt.Errorf("PDF 页树存在循环或重复页面")
		}
		seen[id] = true
	}
	dict, err := v.dict(object)
	if err != nil || dict.NameEntry("Type") == nil {
		return 0, fmt.Errorf("PDF 页树结构无效")
	}
	switch *dict.NameEntry("Type") {
	case "Page":
		return 1, nil
	case "Pages":
		object, err := v.resolve(dict["Kids"])
		kids, ok := object.(types.Array)
		if err != nil || !ok || len(kids) == 0 {
			return 0, fmt.Errorf("PDF 页树缺少页面")
		}
		total := 0
		for _, kid := range kids {
			count, err := v.pages(kid, depth+1, seen)
			if err != nil {
				return 0, err
			}
			total += count
			if total > ticketPDFMaxObjects {
				return 0, fmt.Errorf("PDF 页面过多")
			}
		}
		count, err := v.resolve(dict["Count"])
		declared, ok := count.(types.Integer)
		if err != nil || !ok || int(declared) != total {
			return 0, fmt.Errorf("PDF 页面数量无效")
		}
		return total, nil
	default:
		return 0, fmt.Errorf("PDF 页树节点无效")
	}
}
