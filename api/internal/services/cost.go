package services

import (
	"fmt"
	"time"
)

// What running something costs.
//
// There is no billing API to ask, and self-hosted Kubernetes has no notion of a price. So
// this multiplies what workloads reserve by a rate card, and the honest framing is that it
// is an estimate whose accuracy is entirely the accuracy of the rates given to it.
//
// The basis is requests, not usage. A 500m request occupies 500m of a node whether or not it
// is used, and that is what the cluster owner is paying for. Usage is reported alongside, as
// efficiency, because the gap between the two is the actionable number -- an app on a large
// size using five per cent of it is the finding worth surfacing.

// RateCard prices the units a workload reserves.
type RateCard struct {
	CPUCoreHour     float64 `json:"cpuCoreHour"`
	MemoryGiBHour   float64 `json:"memoryGiBHour"`
	StorageGiBMonth float64 `json:"storageGiBMonth"`
	Currency        string  `json:"currency"`
}

// hoursPerMonth is the conventional 730 -- 365 days / 12 months -- which is what cloud
// providers bill against.
const hoursPerMonth = 730.0

// DefaultRateCard is a starting point, not a claim.
//
// Derived from one commodity node so the numbers can be checked rather than taken on trust:
// an 8 vCPU / 32 GiB machine at $200 a month is $200/730 = $0.2740 an hour. That hourly cost
// is split evenly between CPU and memory, giving $0.1370 an hour for each half:
//
//	CPU:    $0.1370 / 8 vCPU  = $0.01712 per vCPU-hour
//	Memory: $0.1370 / 32 GiB  = $0.00428 per GiB-hour
//
// The even split is a convention, not a fact -- a memory-heavy fleet is really paying more
// for memory than this says. It exists so the total is not double-counted, which is the
// mistake that makes a naive rate card report roughly twice the true spend: pricing CPU at
// the whole node cost and memory at the whole node cost as well.
//
// Storage is priced separately because it is bought separately.
var DefaultRateCard = RateCard{
	CPUCoreHour:     0.01712,
	MemoryGiBHour:   0.00428,
	StorageGiBMonth: 0.10,
	Currency:        "USD",
}

// RatesFromNode derives a rate card from what a node actually costs.
//
// This exists because "what does a node cost me" is a question an administrator can answer
// and "what is a vCPU-hour worth" is not. Asking the answerable question is the difference
// between a cost figure somebody trusts and one they ignore.
func RatesFromNode(monthlyCost, vcpus, memoryGiB float64) (RateCard, error) {
	if monthlyCost <= 0 {
		return RateCard{}, fmt.Errorf("a node's monthly cost must be positive")
	}
	if vcpus <= 0 || memoryGiB <= 0 {
		return RateCard{}, fmt.Errorf("a node needs both vCPUs and memory to split its cost between")
	}

	hourly := monthlyCost / hoursPerMonth
	half := hourly / 2

	return RateCard{
		CPUCoreHour:     half / vcpus,
		MemoryGiBHour:   half / memoryGiB,
		StorageGiBMonth: DefaultRateCard.StorageGiBMonth,
		Currency:        DefaultRateCard.Currency,
	}, nil
}

// Reservation is what one workload holds over a period.
type Reservation struct {
	CPUMillicores int64
	MemoryBytes   int64
	StorageBytes  int64
	Replicas      int64
	// Duration is how long it was held. A sample represents the interval since the last
	// one, so a workload that existed for ten minutes of an hour is charged for ten.
	Duration time.Duration
}

// Cost is a priced reservation.
type Cost struct {
	CPU      float64 `json:"cpu"`
	Memory   float64 `json:"memory"`
	Storage  float64 `json:"storage"`
	Total    float64 `json:"total"`
	Currency string  `json:"currency"`
}

// ComputeCost prices a reservation.
func ComputeCost(r Reservation, rates RateCard) Cost {
	if rates.Currency == "" {
		rates.Currency = DefaultRateCard.Currency
	}

	replicas := float64(r.Replicas)
	if replicas <= 0 {
		// A scaled-to-zero workload reserves nothing, which is the entire point of
		// scale-to-zero and has to show up here as actually costing nothing.
		replicas = 0
	}

	hours := r.Duration.Hours()
	cores := float64(r.CPUMillicores) / 1000
	memGiB := float64(r.MemoryBytes) / (1 << 30)
	storageGiB := float64(r.StorageBytes) / (1 << 30)

	c := Cost{Currency: rates.Currency}
	c.CPU = cores * replicas * rates.CPUCoreHour * hours
	c.Memory = memGiB * replicas * rates.MemoryGiBHour * hours

	// Storage is priced per month and is not multiplied by replicas: a StatefulSet's claims
	// are per replica, but an app's volume is one volume however many pods mount it, and
	// the sampler records what actually exists rather than what is templated.
	c.Storage = storageGiB * (rates.StorageGiBMonth / hoursPerMonth) * hours

	c.Total = c.CPU + c.Memory + c.Storage
	return c
}

// Add sums two costs, for rolling app figures up to an environment and a project.
func (c Cost) Add(other Cost) Cost {
	currency := c.Currency
	if currency == "" {
		currency = other.Currency
	}
	return Cost{
		CPU:      c.CPU + other.CPU,
		Memory:   c.Memory + other.Memory,
		Storage:  c.Storage + other.Storage,
		Total:    c.Total + other.Total,
		Currency: currency,
	}
}

// Projected extrapolates a cost measured over one window to a month.
//
// Separate from the measured figure and labelled as such, because the two answer different
// questions -- "what did last week cost" and "what will this cost if nothing changes" -- and
// presenting an extrapolation as a measurement is how a cost page loses trust.
func (c Cost) Projected(window time.Duration) Cost {
	if window <= 0 {
		return Cost{Currency: c.Currency}
	}
	factor := (hoursPerMonth * float64(time.Hour)) / float64(window)
	return Cost{
		CPU:      c.CPU * factor,
		Memory:   c.Memory * factor,
		Storage:  c.Storage * factor,
		Total:    c.Total * factor,
		Currency: c.Currency,
	}
}

// Efficiency is the fraction of a reservation actually used, or -1 when it cannot be known.
//
// The gap is the actionable number: an app reserving a large size and using five per cent of
// it is the finding worth surfacing, and it is invisible if only the bill is shown.
func Efficiency(reserved, used int64) float64 {
	if reserved <= 0 {
		return -1
	}
	return float64(used) / float64(reserved)
}
