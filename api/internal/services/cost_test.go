package services

import (
	"math"
	"testing"
	"time"
)

func closeTo(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.0001 {
		t.Errorf("%s = %f, want %f", label, got, want)
	}
}

// The mistake this guards against makes a rate card report roughly twice the true spend:
// pricing CPU at the whole node cost and memory at the whole node cost as well, so one node
// is billed twice.
//
// A full node's worth of reservations must price back to the node's own cost.
func TestRatesFromNodeDoNotDoubleCountTheNode(t *testing.T) {
	const monthly, vcpus, memGiB = 200.0, 8.0, 32.0

	rates, err := RatesFromNode(monthly, vcpus, memGiB)
	if err != nil {
		t.Fatalf("RatesFromNode: %v", err)
	}

	// One node, fully reserved, for one hour.
	full := ComputeCost(Reservation{
		CPUMillicores: int64(vcpus * 1000),
		MemoryBytes:   int64(memGiB) * (1 << 30),
		Replicas:      1,
		Duration:      time.Hour,
	}, rates)

	closeTo(t, "a fully reserved node for an hour", full.Total, monthly/hoursPerMonth)

	// And a month of it prices back to the node's monthly cost.
	closeTo(t, "a month of it", full.Projected(time.Hour).Total, monthly)
}

func TestDefaultRateCardMatchesItsStatedDerivation(t *testing.T) {
	derived, err := RatesFromNode(200, 8, 32)
	if err != nil {
		t.Fatalf("RatesFromNode: %v", err)
	}

	// The doc comment claims the defaults come from an 8 vCPU / 32 GiB node at $200. If the
	// two drift, the explanation is wrong and nobody can check the numbers.
	closeTo(t, "default CPU rate", DefaultRateCard.CPUCoreHour, derived.CPUCoreHour)
	closeTo(t, "default memory rate", DefaultRateCard.MemoryGiBHour, derived.MemoryGiBHour)
}

func TestRatesFromNodeRejectsNonsense(t *testing.T) {
	for _, c := range []struct{ monthly, vcpus, mem float64 }{
		{0, 8, 32}, {-1, 8, 32}, {200, 0, 32}, {200, 8, 0}, {200, -4, 32},
	} {
		if _, err := RatesFromNode(c.monthly, c.vcpus, c.mem); err == nil {
			t.Errorf("RatesFromNode(%v, %v, %v) was accepted", c.monthly, c.vcpus, c.mem)
		}
	}
}

// Scale-to-zero has to show up as actually costing nothing, or the feature's whole point is
// invisible on the page where it would be most convincing.
func TestScaledToZeroCostsNothing(t *testing.T) {
	rates := DefaultRateCard
	asleep := ComputeCost(Reservation{
		CPUMillicores: 500,
		MemoryBytes:   1 << 30,
		Replicas:      0,
		Duration:      time.Hour,
	}, rates)

	if asleep.CPU != 0 || asleep.Memory != 0 {
		t.Errorf("a scaled-to-zero app was charged for compute: %+v", asleep)
	}
}

// Storage is charged whether or not anything is running: a volume that exists is a volume
// being paid for, and that is exactly the cost a sleeping app still carries.
func TestStorageIsChargedIndependentlyOfReplicas(t *testing.T) {
	rates := DefaultRateCard
	r := Reservation{StorageBytes: 10 * (1 << 30), Replicas: 0, Duration: hoursPerMonth * time.Hour}

	c := ComputeCost(r, rates)
	closeTo(t, "ten GiB for a month", c.Storage, 10*rates.StorageGiBMonth)
	if c.Total != c.Storage {
		t.Error("a workload with no replicas was charged for something other than storage")
	}
}

func TestCostScalesWithReplicasAndTime(t *testing.T) {
	rates := RateCard{CPUCoreHour: 1, MemoryGiBHour: 1, Currency: "USD"}
	base := Reservation{CPUMillicores: 1000, MemoryBytes: 1 << 30, Replicas: 1, Duration: time.Hour}

	one := ComputeCost(base, rates)
	closeTo(t, "one core and one GiB for an hour", one.Total, 2)

	base.Replicas = 3
	closeTo(t, "three replicas", ComputeCost(base, rates).Total, 6)

	base.Replicas = 1
	base.Duration = 30 * time.Minute
	closeTo(t, "half an hour", ComputeCost(base, rates).Total, 1)
}

// A projection is a different question from a measurement, and labelling one as the other is
// how a cost page loses trust.
func TestProjection(t *testing.T) {
	measured := Cost{Total: 1, CPU: 1, Currency: "USD"}

	month := measured.Projected(time.Hour)
	closeTo(t, "an hour projected to a month", month.Total, hoursPerMonth)

	week := Cost{Total: 70}.Projected(7 * 24 * time.Hour)
	closeTo(t, "a week projected to a month", week.Total, 70*(hoursPerMonth/168))

	// A zero window cannot be extrapolated from, and must not divide by zero.
	if got := measured.Projected(0); got.Total != 0 || math.IsInf(got.Total, 0) {
		t.Errorf("projecting over no window gave %v", got.Total)
	}
}

func TestAddRollsUp(t *testing.T) {
	a := Cost{CPU: 1, Memory: 2, Storage: 3, Total: 6, Currency: "USD"}
	b := Cost{CPU: 0.5, Memory: 0.5, Storage: 1, Total: 2}

	sum := a.Add(b)
	closeTo(t, "total", sum.Total, 8)
	if sum.Currency != "USD" {
		t.Errorf("currency = %q, want it carried through the roll-up", sum.Currency)
	}

	// Summing onto a zero value must not lose the currency either.
	if got := (Cost{}).Add(a); got.Currency != "USD" {
		t.Errorf("currency = %q when adding to an empty cost", got.Currency)
	}
}

// The gap between reserved and used is the actionable number. An app on a large size using
// five per cent of it is the finding, and it is invisible if only the bill is shown.
func TestEfficiency(t *testing.T) {
	if got := Efficiency(1000, 50); math.Abs(got-0.05) > 0.0001 {
		t.Errorf("Efficiency(1000, 50) = %f, want 0.05", got)
	}
	if got := Efficiency(1000, 1000); got != 1 {
		t.Errorf("Efficiency at full use = %f, want 1", got)
	}
	// Unknowable rather than zero: reserving nothing is not the same as wasting everything.
	if got := Efficiency(0, 0); got != -1 {
		t.Errorf("Efficiency with no reservation = %f, want -1 for unknown", got)
	}
}
