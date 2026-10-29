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

// Negative reports whether any component of r is negative.
func (r Resource) Negative() bool {
	return r.CPU < 0 || r.MemoryMB < 0 || r.DiskGB < 0 || r.GPUs < 0
}

// Add returns the component-wise sum of r and o.
func (r Resource) Add(o Resource) Resource {
	return Resource{
		CPU:      r.CPU + o.CPU,
		MemoryMB: r.MemoryMB + o.MemoryMB,
		DiskGB:   r.DiskGB + o.DiskGB,
		GPUs:     r.GPUs + o.GPUs,
	}
}

// Sub returns the component-wise difference r minus o, clamped at zero
// so a release larger than the allocation never goes negative.
func (r Resource) Sub(o Resource) Resource {
	out := Resource{
		CPU:      r.CPU - o.CPU,
		MemoryMB: r.MemoryMB - o.MemoryMB,
		DiskGB:   r.DiskGB - o.DiskGB,
		GPUs:     r.GPUs - o.GPUs,
	}
	if out.CPU < 0 {
		out.CPU = 0
	}
	if out.MemoryMB < 0 {
		out.MemoryMB = 0
	}
	if out.DiskGB < 0 {
		out.DiskGB = 0
	}
	if out.GPUs < 0 {
		out.GPUs = 0
	}
	return out
}

// String returns a short human-readable representation of the resource.
func (r Resource) String() string {
	return fmt.Sprintf("%dcpu/%dMB/%dGB/%dgpu", r.CPU, r.MemoryMB, r.DiskGB, r.GPUs)
}
