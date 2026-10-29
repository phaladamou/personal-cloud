// Package gpu models the accelerators of a compute node and discovers
// the local ones on a best-effort basis.
//
// An Inventory is the list of Devices (model name and memory) visible
// on one machine. Discover builds it from nvidia-smi when the tool is
// present and answers in time; any failure (missing binary, timeout,
// unparsable output) yields an empty inventory, never an error, so
// callers on CPU-only machines need no special case. Fake builds
// scripted inventories for tests.
//
// The scheduler consumes GPU requirements through types.Resource.GPUs
// (count-based fit on allocatable capacity); Labels exposes the
// inventory as node labels so a GPU model can additionally pin
// placement through a label selector.
package gpu

import (
	"bytes"
	"context"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// discoverTimeout bounds the nvidia-smi probe: discovery must stay a
// fast best-effort hook, never a slow startup dependency.
const discoverTimeout = 5 * time.Second

// Device is one accelerator visible on a machine.
type Device struct {
	// Index is the nvidia-smi GPU index (0-based, discovery order).
	Index int `json:"index"`
	// Model is the product name as reported by nvidia-smi
	// (for example "NVIDIA T4").
	Model string `json:"model"`
	// MemoryMB is the total device memory in MiB.
	MemoryMB int64 `json:"memory_mb"`
}

// Inventory is the set of accelerators of one machine. The zero value
// is an empty (GPU-less) inventory.
type Inventory struct {
	Devices []Device `json:"devices"`
}

// Count returns the number of accelerators in the inventory.
func (in Inventory) Count() int {
	return len(in.Devices)
}

// TotalMemoryMB returns the summed device memory in MiB.
func (in Inventory) TotalMemoryMB() int64 {
	var total int64
	for _, d := range in.Devices {
		total += d.MemoryMB
	}
	return total
}

// Models returns the sorted unique model names of the inventory.
func (in Inventory) Models() []string {
	seen := make(map[string]bool, len(in.Devices))
	for _, d := range in.Devices {
		seen[d.Model] = true
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// Labels exposes the inventory as node labels: gpu.count always, plus
// gpu.model and gpu.memory_mb when the inventory holds exactly one
// model. An empty inventory yields no labels.
func Labels(in Inventory) map[string]string {
	if len(in.Devices) == 0 {
		return nil
	}
	out := map[string]string{"gpu.count": strconv.Itoa(len(in.Devices))}
	if models := in.Models(); len(models) == 1 {
		out["gpu.model"] = models[0]
		out["gpu.memory_mb"] = strconv.FormatInt(in.TotalMemoryMB(), 10)
	}
	return out
}

// Fake builds a scripted inventory from model names, each device
// carrying memoryMB. It is the deterministic stand-in for Discover in
// tests and on machines without accelerators.
func Fake(memoryMB int64, models ...string) Inventory {
	out := Inventory{}
	for i, m := range models {
		out.Devices = append(out.Devices, Device{Index: i, Model: m, MemoryMB: memoryMB})
	}
	return out
}

// FakeDiscoverer returns a Discover-compatible func yielding inv on
// every call.
func FakeDiscoverer(inv Inventory) func() Inventory {
	return func() Inventory { return inv }
}

// Discover probes the local accelerators via nvidia-smi. It never
// fails: any problem yields an empty inventory.
func Discover() Inventory {
	ctx, cancel := context.WithTimeout(context.Background(), discoverTimeout)
	defer cancel()
	return discoverWithRunner(func(name string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			return "", err
		}
		return stdout.String(), nil
	})
}

// discoverWithRunner runs the nvidia-smi probe through run and parses
// its output. Every failure maps to an empty inventory.
func discoverWithRunner(run func(name string, args ...string) (string, error)) Inventory {
	raw, err := run("nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits")
	if err != nil {
		return Inventory{}
	}
	return parseNvidiaSMI(raw)
}

// parseNvidiaSMI decodes the "name, memory.total" CSV rows of
// nvidia-smi, skipping malformed lines. Memory is in MiB.
func parseNvidiaSMI(raw string) Inventory {
	var out Inventory
	for _, line := range strings.Split(raw, "\n") {
		name, mem, ok := strings.Cut(line, ",")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		memoryMB, err := strconv.ParseInt(strings.TrimSpace(mem), 10, 64)
		if name == "" || err != nil || memoryMB <= 0 {
			continue
		}
		out.Devices = append(out.Devices, Device{
			Index:    len(out.Devices),
			Model:    name,
			MemoryMB: memoryMB,
		})
	}
	return out
}
