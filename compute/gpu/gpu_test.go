package gpu

import (
	"errors"
	"reflect"
	"testing"
)

func TestInventoryAggregates(t *testing.T) {
	inv := Fake(15360, "NVIDIA T4", "NVIDIA T4", "NVIDIA L4")
	if got := inv.Count(); got != 3 {
		t.Fatalf("Count: got %d, want 3", got)
	}
	if got := inv.TotalMemoryMB(); got != 3*15360 {
		t.Fatalf("TotalMemoryMB: got %d, want %d", got, 3*15360)
	}
	if got, want := inv.Models(), []string{"NVIDIA L4", "NVIDIA T4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Models: got %v, want %v", got, want)
	}
	var empty Inventory
	if empty.Count() != 0 || empty.TotalMemoryMB() != 0 || len(empty.Models()) != 0 {
		t.Fatalf("empty inventory aggregates: got %+v", empty)
	}
}

func TestLabels(t *testing.T) {
	single := Fake(15360, "NVIDIA T4", "NVIDIA T4")
	got := Labels(single)
	want := map[string]string{"gpu.count": "2", "gpu.model": "NVIDIA T4", "gpu.memory_mb": "30720"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Labels homogeneous: got %v, want %v", got, want)
	}
	mixed := Fake(8192, "NVIDIA T4", "NVIDIA L4")
	got = Labels(mixed)
	want = map[string]string{"gpu.count": "2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Labels mixed: got %v, want %v", got, want)
	}
	if got := Labels(Inventory{}); got != nil {
		t.Fatalf("Labels empty: got %v, want nil", got)
	}
}

func TestParseNvidiaSMI(t *testing.T) {
	raw := "NVIDIA T4, 15360\nNVIDIA L4, 23040\n"
	inv := parseNvidiaSMI(raw)
	if inv.Count() != 2 {
		t.Fatalf("Count: got %d, want 2", inv.Count())
	}
	if inv.Devices[0] != (Device{Index: 0, Model: "NVIDIA T4", MemoryMB: 15360}) {
		t.Fatalf("device 0: got %+v", inv.Devices[0])
	}
	if inv.Devices[1] != (Device{Index: 1, Model: "NVIDIA L4", MemoryMB: 23040}) {
		t.Fatalf("device 1: got %+v", inv.Devices[1])
	}
}

func TestParseNvidiaSMISkipsMalformedLines(t *testing.T) {
	raw := "\nTesla V100-SXM2-16GB, 16160\nbroken line\n, 100\nKepler, not-a-number\nZero, 0\n"
	inv := parseNvidiaSMI(raw)
	if inv.Count() != 1 {
		t.Fatalf("Count: got %d (%+v), want 1", inv.Count(), inv.Devices)
	}
	if inv.Devices[0].Model != "Tesla V100-SXM2-16GB" || inv.Devices[0].MemoryMB != 16160 {
		t.Fatalf("device: got %+v", inv.Devices[0])
	}
}

func TestDiscoverWithRunner(t *testing.T) {
	inv := discoverWithRunner(func(name string, args ...string) (string, error) {
		if name != "nvidia-smi" {
			t.Fatalf("binary: got %q, want nvidia-smi", name)
		}
		return "NVIDIA T4, 15360\n", nil
	})
	if inv.Count() != 1 || inv.Devices[0].Model != "NVIDIA T4" {
		t.Fatalf("discovered: got %+v", inv)
	}
}

func TestDiscoverWithRunnerNeverFails(t *testing.T) {
	runners := map[string]func(string, ...string) (string, error){
		"missing binary": func(string, ...string) (string, error) {
			return "", errors.New("exec: nvidia-smi not found")
		},
		"garbage output": func(string, ...string) (string, error) {
			return "not csv at all\n", nil
		},
		"empty output": func(string, ...string) (string, error) {
			return "", nil
		},
	}
	for name, run := range runners {
		if got := discoverWithRunner(run); got.Count() != 0 {
			t.Fatalf("%s: got %+v, want empty inventory", name, got)
		}
	}
}

// TestDiscoverNeverFails pins the hook contract on the real machine:
// whatever the local hardware, Discover returns (possibly empty)
// without error or panic.
func TestDiscoverNeverFails(t *testing.T) {
	inv := Discover()
	if inv.Count() < 0 || inv.TotalMemoryMB() < 0 {
		t.Fatalf("Discover: got %+v", inv)
	}
}

func TestFakeDiscoverer(t *testing.T) {
	want := Fake(1024, "Fake GPU")
	if got := FakeDiscoverer(want)(); !reflect.DeepEqual(got, want) {
		t.Fatalf("FakeDiscoverer: got %+v, want %+v", got, want)
	}
}
