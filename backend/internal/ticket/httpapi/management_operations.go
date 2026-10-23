package httpapi

import "github.com/TokenFlux/TokenRouter/internal/server/adminapi"

// management_operations.go registers browser continuations for this module in
// the MCP management catalogue. Support conversations can contain credentials
// and private multipart attachments. Administrators complete these operations
// in the existing support page, where conversation content, file selection,
// and attachment previews use authenticated browser requests.

// ManagementOperations sends support configuration, conversations, and private
// attachment actions to the support pages for completion in the administrator browser.
// @project-doc docs/interfaces/mcp_management.md#fork_management
var ManagementOperations = map[string]*adminapi.Operation{
	"tickets.settings.get": adminapi.Define(adminapi.Operation{
		ID:     "tickets.settings.get",
		Module: "tickets",
		Title:  "View support settings",
		Description: "View support settings in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "GET",
		Path:        "/api/v1/admin/tickets/settings",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/settings?tab=tickets",
		Input:       adminapi.Arguments("/api/v1/admin/tickets/settings", nil, nil),
	}),
	"tickets.settings.update": adminapi.Define(adminapi.Operation{
		ID:     "tickets.settings.update",
		Module: "tickets",
		Title:  "Update support settings",
		Description: "Update support settings in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "PUT",
		Path:        "/api/v1/admin/tickets/settings",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/settings?tab=tickets",
		Input:       adminapi.Arguments("/api/v1/admin/tickets/settings", nil, nil),
	}),
	"tickets.list": adminapi.Define(adminapi.Operation{
		ID:     "tickets.list",
		Module: "tickets",
		Title:  "View the support queue",
		Description: "View the support queue in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "GET",
		Path:        "/api/v1/admin/tickets",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/tickets",
		Input:       adminapi.Arguments("/api/v1/admin/tickets", nil, nil),
	}),
	"tickets.get": adminapi.Define(adminapi.Operation{
		ID:     "tickets.get",
		Module: "tickets",
		Title:  "View a support conversation",
		Description: "View a support conversation in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "GET",
		Path:        "/api/v1/admin/tickets/:id",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/tickets",
		Input:       adminapi.Arguments("/api/v1/admin/tickets/:id", nil, nil),
	}),
	"tickets.update": adminapi.Define(adminapi.Operation{
		ID:     "tickets.update",
		Module: "tickets",
		Title:  "Update ticket priority",
		Description: "Update ticket priority in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "PATCH",
		Path:        "/api/v1/admin/tickets/:id",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/tickets",
		Input:       adminapi.Arguments("/api/v1/admin/tickets/:id", nil, nil),
	}),
	"tickets.reply": adminapi.Define(adminapi.Operation{
		ID:     "tickets.reply",
		Module: "tickets",
		Title:  "Reply to a support conversation",
		Description: "Reply to a support conversation in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "POST",
		Path:        "/api/v1/admin/tickets/:id/replies",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/tickets",
		Input:       adminapi.Arguments("/api/v1/admin/tickets/:id/replies", nil, nil),
	}),
	"tickets.complete": adminapi.Define(adminapi.Operation{
		ID:     "tickets.complete",
		Module: "tickets",
		Title:  "Complete a support conversation",
		Description: "Complete a support conversation in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "POST",
		Path:        "/api/v1/admin/tickets/:id/complete",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/tickets",
		Input:       adminapi.Arguments("/api/v1/admin/tickets/:id/complete", nil, nil),
	}),
	"tickets.cancel": adminapi.Define(adminapi.Operation{
		ID:     "tickets.cancel",
		Module: "tickets",
		Title:  "Cancel a support conversation",
		Description: "Cancel a support conversation in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "POST",
		Path:        "/api/v1/admin/tickets/:id/cancel",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/tickets",
		Input:       adminapi.Arguments("/api/v1/admin/tickets/:id/cancel", nil, nil),
	}),
	"tickets.attachments.download": adminapi.Define(adminapi.Operation{
		ID:     "tickets.attachments.download",
		Module: "tickets",
		Title:  "Download a private support attachment",
		Description: "Download a private support attachment in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "GET",
		Path:        "/api/v1/admin/tickets/:id/attachments/:attachmentId",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/tickets",
		Input:       adminapi.Arguments("/api/v1/admin/tickets/:id/attachments/:attachmentId", nil, nil),
	}),
	"tickets.attachments.preview": adminapi.Define(adminapi.Operation{
		ID:     "tickets.attachments.preview",
		Module: "tickets",
		Title:  "Preview a private support attachment",
		Description: "Preview a private support attachment in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "GET",
		Path:        "/api/v1/admin/tickets/:id/attachments/:attachmentId/preview",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/tickets",
		Input:       adminapi.Arguments("/api/v1/admin/tickets/:id/attachments/:attachmentId/preview", nil, nil),
	}),
}
