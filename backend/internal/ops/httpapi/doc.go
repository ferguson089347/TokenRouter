// Package httpapi exposes authenticated operational monitoring and system
// maintenance through Gin handlers. Application routers supply administrator
// authentication, audit capture, panel limits, and sensitive-action guards;
// passive channel user routes additionally resolve current group permissions.
//
// Start reading at:
//   - routes_admin.go: administrator monitoring and maintenance registration.
//   - ops_handler.go: request and upstream error lookup and resolution.
//   - monitor.go: passive channel queries, configuration, and viewer scoping.
//
// File groups:
//   - ops_dashboard_handler.go and ops_snapshot_v2_handler.go: dashboard windows and combined snapshots.
//   - ops_realtime_handler.go, ops_ws_handler.go, and ops_auth_cache_health_handler.go: current concurrency, traffic, and authentication cache health.
//   - ops_alerts_handler.go and ops_settings_handler.go: alert administration and persisted monitoring settings.
//   - ops_ingress_reject_handler.go and ops_system_log_handler.go: ingress rejection and system log queries.
//   - monitor*.go: passive channel reads, configuration, scope, and registered management operations.
//   - system.go and management_operations.go: system actions and administrator permission metadata.
package httpapi
