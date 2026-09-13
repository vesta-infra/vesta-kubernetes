package handlers

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The chart shipped three presets while this file listed six, so which sizes an operator
// could choose depended on whether a VestaConfig happened to exist. They have to agree.
func TestDefaultPodSizesMatchChartValues(t *testing.T) {
	path := filepath.Join("..", "..", "..", "deploy", "helm", "vesta", "values.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("chart values not readable from here: %v", err)
	}

	// Read the names out of the podSizeList block without a YAML dependency: the block
	// runs until the next key at two-space indent.
	var inBlock bool
	chart := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "  podSizeList:") {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		if line != "" && !strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "  -") {
			break
		}
		if idx := strings.Index(line, "- name:"); idx != -1 {
			chart[strings.TrimSpace(line[idx+len("- name:"):])] = true
		}
	}

	if len(chart) == 0 {
		t.Fatal("found no presets in the chart's podSizeList")
	}

	code := map[string]bool{}
	for _, p := range defaultPodSizes {
		code[p["name"].(string)] = true
	}

	for name := range code {
		if !chart[name] {
			t.Errorf("%q is a built-in default but missing from the chart", name)
		}
	}
	for name := range chart {
		if !code[name] {
			t.Errorf("%q is in the chart but missing from defaultPodSizes", name)
		}
	}
}

// A preset with no CPU request lets a pod be scheduled anywhere and then throttled, and a
// preset with no memory limit lets one pod take a node down. Every preset needs all four.
func TestEveryPodSizeIsComplete(t *testing.T) {
	for _, p := range defaultPodSizes {
		name, _ := p["name"].(string)
		for _, field := range []string{"cpu", "memory", "cpuLimit", "memoryLimit"} {
			if v, ok := p[field].(string); !ok || v == "" {
				t.Errorf("preset %q is missing %s", name, field)
			}
		}
	}
}

// The point of the mem- family is a lower CPU-to-memory ratio than the balanced sizes.
// A mem- preset that is not actually memory-heavy is just a confusing duplicate.
func TestMemoryOptimisedSizesFavourMemory(t *testing.T) {
	ratio := func(p map[string]interface{}) float64 {
		return millicores(p["cpu"].(string)) / mebibytes(p["memory"].(string))
	}

	var balanced, memory []float64
	for _, p := range defaultPodSizes {
		if strings.HasPrefix(p["name"].(string), "mem-") {
			memory = append(memory, ratio(p))
		} else {
			balanced = append(balanced, ratio(p))
		}
	}
	if len(memory) == 0 {
		t.Fatal("no mem- presets found")
	}

	// Every memory-optimised preset must sit below every balanced one on CPU per MiB.
	worstMem, bestBalanced := 0.0, 1e9
	for _, r := range memory {
		if r > worstMem {
			worstMem = r
		}
	}
	for _, r := range balanced {
		if r < bestBalanced {
			bestBalanced = r
		}
	}
	if worstMem >= bestBalanced {
		t.Errorf("a mem- preset has CPU/memory %.4f, not below every balanced preset (%.4f)", worstMem, bestBalanced)
	}
}

func millicores(s string) float64 {
	if strings.HasSuffix(s, "m") {
		return parseFloat(strings.TrimSuffix(s, "m"))
	}
	return parseFloat(s) * 1000
}

func mebibytes(s string) float64 {
	switch {
	case strings.HasSuffix(s, "Gi"):
		return parseFloat(strings.TrimSuffix(s, "Gi")) * 1024
	case strings.HasSuffix(s, "Mi"):
		return parseFloat(strings.TrimSuffix(s, "Mi"))
	}
	return parseFloat(s)
}

func parseFloat(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}
