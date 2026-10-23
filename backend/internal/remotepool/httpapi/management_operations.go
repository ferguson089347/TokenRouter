package httpapi

import "github.com/TokenFlux/TokenRouter/internal/server/adminapi"

// management_operations.go registers browser continuations for this module in
// the MCP management catalogue. Remote pool configuration includes rotating
// source credentials and local group bindings. Administrators complete these
// operations in the existing settings page, where credential fields and
// binding choices share the current validation flow.

// ManagementOperations sends source credential settings and group binding requests
// to the pool settings page for completion in the administrator browser.
// @project-doc docs/interfaces/mcp_management.md#fork_management
var ManagementOperations = map[string]*adminapi.Operation{
	"remote_pool.settings.get": adminapi.Define(adminapi.Operation{
		ID:     "remote_pool.settings.get",
		Module: "remote_pool",
		Title:  "View remote pool settings",
		Description: "View remote pool settings in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "GET",
		Path:        "/api/v1/admin/remote-account-pool/settings",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/remote-account-pool",
		Input:       adminapi.Arguments("/api/v1/admin/remote-account-pool/settings", nil, nil),
	}),
	"remote_pool.settings.update": adminapi.Define(adminapi.Operation{
		ID:     "remote_pool.settings.update",
		Module: "remote_pool",
		Title:  "Update remote pool settings",
		Description: "Update remote pool settings in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "PUT",
		Path:        "/api/v1/admin/remote-account-pool/settings",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/remote-account-pool",
		Input:       adminapi.Arguments("/api/v1/admin/remote-account-pool/settings", nil, nil),
	}),
	"remote_pool.source_groups.list": adminapi.Define(adminapi.Operation{
		ID:     "remote_pool.source_groups.list",
		Module: "remote_pool",
		Title:  "View source group bindings",
		Description: "View source group bindings in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "GET",
		Path:        "/api/v1/admin/remote-account-pool/source-groups",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/remote-account-pool",
		Input:       adminapi.Arguments("/api/v1/admin/remote-account-pool/source-groups", nil, nil),
	}),
}
