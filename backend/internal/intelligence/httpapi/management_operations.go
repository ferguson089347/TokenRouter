package httpapi

import "github.com/TokenFlux/TokenRouter/internal/server/adminapi"

// management_operations.go registers browser continuations for this module in
// the MCP management catalogue. Quality checks use personal gateway
// credentials and display generated HTML through signed previews.
// Administrators complete configuration, submissions, and result inspection in
// the existing page, which supplies the credential and preview controls.

// ManagementOperations identifies browser actions for configuring quality checks
// and inspecting their execution histories, generated previews, and saved screenshots.
// @project-doc docs/interfaces/mcp_management.md#fork_management
var ManagementOperations = map[string]*adminapi.Operation{
	"intelligence.configs.list": adminapi.Define(adminapi.Operation{
		ID:     "intelligence.configs.list",
		Module: "intelligence",
		Title:  "View quality check configurations",
		Description: "View quality check configurations in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "GET",
		Path:        "/api/v1/admin/intelligence-tests",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/intelligence-tests",
		Input:       adminapi.Arguments("/api/v1/admin/intelligence-tests", nil, nil),
	}),
	"intelligence.configs.create": adminapi.Define(adminapi.Operation{
		ID:     "intelligence.configs.create",
		Module: "intelligence",
		Title:  "Create a quality check configuration",
		Description: "Create a quality check configuration in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "POST",
		Path:        "/api/v1/admin/intelligence-tests",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/intelligence-tests",
		Input:       adminapi.Arguments("/api/v1/admin/intelligence-tests", nil, nil),
	}),
	"intelligence.runs.get": adminapi.Define(adminapi.Operation{
		ID:     "intelligence.runs.get",
		Module: "intelligence",
		Title:  "View a quality check result",
		Description: "View a quality check result in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "GET",
		Path:        "/api/v1/admin/intelligence-tests/runs/:id",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/intelligence-tests",
		Input:       adminapi.Arguments("/api/v1/admin/intelligence-tests/runs/:id", nil, nil),
	}),
	"intelligence.runs.preview": adminapi.Define(adminapi.Operation{
		ID:     "intelligence.runs.preview",
		Module: "intelligence",
		Title:  "Preview a generated drawing",
		Description: "Preview a generated drawing in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "GET",
		Path:        "/api/v1/admin/intelligence-tests/runs/:id/preview",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/intelligence-tests",
		Input:       adminapi.Arguments("/api/v1/admin/intelligence-tests/runs/:id/preview", nil, nil),
	}),
	"intelligence.runs.thumbnail": adminapi.Define(adminapi.Operation{
		ID:     "intelligence.runs.thumbnail",
		Module: "intelligence",
		Title:  "View a drawing screenshot",
		Description: "View a drawing screenshot in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "GET",
		Path:        "/api/v1/admin/intelligence-tests/runs/:id/thumbnail",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/intelligence-tests",
		Input:       adminapi.Arguments("/api/v1/admin/intelligence-tests/runs/:id/thumbnail", nil, nil),
	}),
	"intelligence.configs.update": adminapi.Define(adminapi.Operation{
		ID:     "intelligence.configs.update",
		Module: "intelligence",
		Title:  "Update a quality check configuration",
		Description: "Update a quality check configuration in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "PUT",
		Path:        "/api/v1/admin/intelligence-tests/:id",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/intelligence-tests",
		Input:       adminapi.Arguments("/api/v1/admin/intelligence-tests/:id", nil, nil),
	}),
	"intelligence.configs.delete": adminapi.Define(adminapi.Operation{
		ID:     "intelligence.configs.delete",
		Module: "intelligence",
		Title:  "Delete a quality check configuration",
		Description: "Delete a quality check configuration in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "DELETE",
		Path:        "/api/v1/admin/intelligence-tests/:id",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/intelligence-tests",
		Input:       adminapi.Arguments("/api/v1/admin/intelligence-tests/:id", nil, nil),
	}),
	"intelligence.configs.run": adminapi.Define(adminapi.Operation{
		ID:     "intelligence.configs.run",
		Module: "intelligence",
		Title:  "Run a quality check",
		Description: "Run a quality check in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "POST",
		Path:        "/api/v1/admin/intelligence-tests/:id/run",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/intelligence-tests",
		Input:       adminapi.Arguments("/api/v1/admin/intelligence-tests/:id/run", nil, nil),
	}),
	"intelligence.runs.list": adminapi.Define(adminapi.Operation{
		ID:     "intelligence.runs.list",
		Module: "intelligence",
		Title:  "View quality check history",
		Description: "View quality check history in the administrator browser page. " +
			"Supply request fields and select the target record there.",
		Method:      "GET",
		Path:        "/api/v1/admin/intelligence-tests/:id/runs",
		Response:    adminapi.ResponseBrowser,
		BrowserPath: "/admin/intelligence-tests",
		Input:       adminapi.Arguments("/api/v1/admin/intelligence-tests/:id/runs", nil, nil),
	}),
}
