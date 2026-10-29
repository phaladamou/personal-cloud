// Package gateway fronts the control-plane API with routing, remote
// access and connection tracking.
//
// The Registry maps workspaces to their network backends: a default
// target address per workspace, per-port forwards for workspace HTTP
// services, and open client connections with live counts. The Gateway
// handler wraps the API server, guarding every route with Bearer [REDACTED]
// and reverse-proxying workspace services (see gateway.go).
package gateway
