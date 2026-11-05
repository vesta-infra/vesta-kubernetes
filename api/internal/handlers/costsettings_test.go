package handlers

import "testing"

func f(v float64) *float64 { return &v }

func TestCostPatch(t *testing.T) {
	t.Run("empty resets", func(t *testing.T) {
		got, err := costPatch(costSettingsRequest{})
		if err != nil || got != nil {
			t.Fatalf("got %v, %v; want nil, nil", got, err)
		}
	})
	t.Run("partial node description refused", func(t *testing.T) {
		if _, err := costPatch(costSettingsRequest{NodeMonthlyCost: f(200), NodeVCPUs: f(8)}); err == nil {
			t.Fatal("want an error: rateCard ignores a node without memory")
		}
	})
	t.Run("non-positive node field refused", func(t *testing.T) {
		if _, err := costPatch(costSettingsRequest{NodeMonthlyCost: f(200), NodeVCPUs: f(0), NodeMemoryGiB: f(32)}); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("negative rate refused", func(t *testing.T) {
		if _, err := costPatch(costSettingsRequest{CPUCoreHour: f(-1)}); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("node fields set, others cleared", func(t *testing.T) {
		got, err := costPatch(costSettingsRequest{NodeMonthlyCost: f(200), NodeVCPUs: f(8), NodeMemoryGiB: f(32)})
		if err != nil {
			t.Fatal(err)
		}
		if got["nodeMonthlyCost"] != 200.0 || got["nodeVCPUs"] != 8.0 || got["nodeMemoryGiB"] != 32.0 {
			t.Errorf("node fields = %v", got)
		}
		// Present-and-nil, so the merge patch deletes a rate left over from before.
		for _, k := range []string{"cpuCoreHour", "memoryGiBHour", "storageGiBMonth", "currency"} {
			if v, ok := got[k]; !ok || v != nil {
				t.Errorf("%s = %v (present %v); want present and nil", k, v, ok)
			}
		}
	})
}
