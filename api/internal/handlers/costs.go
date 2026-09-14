package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
	"kubernetes.getvesta.sh/api/internal/services"
)

// Cost reporting.
//
// Money is computed here, at read time, from stored reservations and the current rate card.
// Storing money instead would mean a month's history was a mixture of old and new prices
// that nobody could reconcile; re-pricing the past is at least explicable, and the response
// says which rates it used.

// costEntry is one row of the breakdown.
type costEntry struct {
	Key         string        `json:"key"`
	Environment string        `json:"environment,omitempty"`
	Kind        string        `json:"kind,omitempty"`
	Cost        services.Cost `json:"cost"`
	Projected   services.Cost `json:"projectedMonthly"`
	// Efficiency is what fraction of the reservation was actually used, or null when
	// metrics-server is not installed. The gap is the actionable number.
	CPUEfficiency    *float64 `json:"cpuEfficiency,omitempty"`
	MemoryEfficiency *float64 `json:"memoryEfficiency,omitempty"`
}

// GetProjectCosts reports what a project's workloads cost over a window.
func (h *Handler) GetProjectCosts(c *gin.Context) {
	h.reportCosts(c, c.Param("projectId"), "")
}

// GetAppCosts reports one app's cost.
func (h *Handler) GetAppCosts(c *gin.Context) {
	h.reportCosts(c, "", c.Param("appId"))
}

func (h *Handler) reportCosts(c *gin.Context, projectID, appID string) {
	window, err := parseWindow(c.DefaultQuery("window", "30d"))
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	groupBy := c.DefaultQuery("groupBy", "app")
	rollups, err := h.DB.CostRollups(c.Request.Context(), projectID, appID, groupBy, time.Now().Add(-window))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	rates := h.rateCard(c)

	entries := make([]costEntry, 0, len(rollups))
	total := services.Cost{Currency: rates.Currency}

	for _, r := range rollups {
		cost := services.Cost{
			CPU:      r.CPUCoreHours * rates.CPUCoreHour,
			Memory:   r.MemoryGiBHours * rates.MemoryGiBHour,
			Storage:  r.StorageGiBHours * (rates.StorageGiBMonth / 730),
			Currency: rates.Currency,
		}
		cost.Total = cost.CPU + cost.Memory + cost.Storage

		entry := costEntry{
			Key:         r.Key,
			Environment: r.Environment,
			Kind:        r.Kind,
			Cost:        cost,
			Projected:   cost.Projected(window),
		}
		if r.CPUCoreHours > 0 && r.CPUUsedCoreHours > 0 {
			eff := r.CPUUsedCoreHours / r.CPUCoreHours
			entry.CPUEfficiency = &eff
		}
		if r.MemoryGiBHours > 0 && r.MemoryUsedGiBHours > 0 {
			eff := r.MemoryUsedGiBHours / r.MemoryGiBHours
			entry.MemoryEfficiency = &eff
		}

		entries = append(entries, entry)
		total = total.Add(cost)
	}

	c.JSON(http.StatusOK, gin.H{
		"window":           window.String(),
		"entries":          entries,
		"total":            total,
		"projectedMonthly": total.Projected(window),
		// Returned so the figures can be checked rather than taken on trust, and so it is
		// visible when they are the built-in estimate rather than this cluster's real cost.
		"rates": rates,
		// True when these are the built-in estimate rather than this cluster's real cost.
		"estimated": rates == services.DefaultRateCard,
	})
}

// rateCard reads the configured prices, falling back to the documented default.
func (h *Handler) rateCard(c *gin.Context) services.RateCard {
	configs, err := h.K8s.ListResources(c.Request.Context(), k8s.VestaConfigGVR, "", "")
	if err != nil || configs == nil || len(configs.Items) == 0 {
		return services.DefaultRateCard
	}

	spec, _, _ := unstructuredNestedMap(configs.Items[0].Object, "spec")
	cost, _, _ := unstructuredNestedMap(spec, "cost")
	if cost == nil {
		return services.DefaultRateCard
	}

	// A node price is the answerable question, so it wins when given: an administrator
	// knows what a machine costs and does not know what a vCPU-hour is worth.
	if monthly, ok := floatFrom(cost["nodeMonthlyCost"]); ok {
		vcpus, okCPU := floatFrom(cost["nodeVCPUs"])
		memory, okMem := floatFrom(cost["nodeMemoryGiB"])
		if okCPU && okMem {
			if rates, err := services.RatesFromNode(monthly, vcpus, memory); err == nil {
				if currency := getNestedString(cost, "currency"); currency != "" {
					rates.Currency = currency
				}
				return rates
			}
		}
	}

	rates := services.DefaultRateCard
	if v, ok := floatFrom(cost["cpuCoreHour"]); ok {
		rates.CPUCoreHour = v
	}
	if v, ok := floatFrom(cost["memoryGiBHour"]); ok {
		rates.MemoryGiBHour = v
	}
	if v, ok := floatFrom(cost["storageGiBMonth"]); ok {
		rates.StorageGiBMonth = v
	}
	if currency := getNestedString(cost, "currency"); currency != "" {
		rates.Currency = currency
	}
	return rates
}

func floatFrom(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// parseWindow accepts the shorthand a cost page needs. Go's own parser stops at hours, and
// "30d" is the obvious thing to ask for here.
func parseWindow(s string) (time.Duration, error) {
	switch s {
	case "24h", "1d":
		return 24 * time.Hour, nil
	case "7d":
		return 7 * 24 * time.Hour, nil
	case "30d":
		return 30 * 24 * time.Hour, nil
	case "90d":
		return 90 * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, errBadWindow
	}
	return d, nil
}

var errBadWindow = errors.New("window must be one of 24h, 7d, 30d, 90d, or a Go duration")
