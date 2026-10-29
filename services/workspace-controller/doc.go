// Package controller owns the workspace lifecycle state machine.
//
// It reconciles the desired workspace state (persisted through the Store
// interface) with the actual workload state (driven through the
// container.ContainerBackend runtime interface) and publishes protocol
// events on the EventBus on every externally visible transition.
package controller
