package process

import (
	"time"

	"github.com/phaladamou/personal-cloud/packages/types"
)

// Info is a point-in-time snapshot of a managed process.
type Info struct {
	ID        string
	Command   string
	Args      []string
	Dir       string
	Env       []string
	PID       int
	State     types.ProcessState
	ExitCode  *int
	StartedAt time.Time
}
