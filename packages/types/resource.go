package types

import "fmt"

// Resource describes a compute resource request or allocation
// (CPU, memory, disk, bandwidth, GPU).
type Resource struct {
	CPU      int   `json:"cpu"`
	MemoryMB int64 `json:"memory_mb"`
	DiskGB   int64 `json:"disk_gb"`
	GPUs     int   `json:"gpus"`
}

// NewResource builds a validated resource descriptor. cpu, memoryMB and
// diskGB must be positive; gpus must be non-negative.
func NewResource(cpu int, memoryMB, diskGB int64, gpus int) (Resource, error) {
	if cpu <= 0 {
		return Resource{}, fmt.Errorf("types: cpu must be positive, got %d", cpu)
	}
	if memoryMB <= 0 {
		return Resource{}, fmt.Errorf("types: memory_mb must be positive, got %d", memoryMB)
	}
	if diskGB <= 0 {
		return Resource{}, fmt.Errorf("types: disk_gb must be positive, got %d", diskGB)
	}
	if gpus < 0 {
		return Resource{}, fmt.Errorf("types: gpus must be non-negative, got %d", gpus)
	}
	return Resource{CPU: cpu, MemoryMB: memoryMB, DiskGB: diskGB, GPUs: gpus}, nil
}

// Fits reports whether r fits within the capacity described by cap.
func (r Resource) Fits(cap Resource) bool {
	return r.CPU <= cap.CPU &&
		r.MemoryMB <= cap.MemoryMB &&
		r.DiskGB <= cap.DiskGB &&
		r.GPUs <= cap.GPUs
}

// String returns a short human-readable representation of the resource.
func (r Resource) String() string {
	return fmt.Sprintf("%dcpu/%dMB/%dGB/%dgpu", r.CPU, r.MemoryMB, r.DiskGB, r.GPUs)
}
