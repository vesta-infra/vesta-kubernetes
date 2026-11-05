package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
	"kubernetes.getvesta.sh/api/internal/services"
)

// The rate card the cost pages price against, set from Settings.
//
// It lives at spec.cost on the VestaConfig and used to be settable only by editing that
// resource -- so the cost page's "set your node's cost in the platform config" pointed at
// nothing anyone could find, and every cluster stayed on the generic estimate.

// costSettingsRequest is every field of spec.cost. Pointers so absent is distinguishable
// from zero: absent fields are removed, which is what makes "reset to default" possible.
type costSettingsRequest struct {
	NodeMonthlyCost *float64 `json:"nodeMonthlyCost"`
	NodeVCPUs       *float64 `json:"nodeVCPUs"`
	NodeMemoryGiB   *float64 `json:"nodeMemoryGiB"`
	CPUCoreHour     *float64 `json:"cpuCoreHour"`
	MemoryGiBHour   *float64 `json:"memoryGiBHour"`
	StorageGiBMonth *float64 `json:"storageGiBMonth"`
	Currency        *string  `json:"currency"`
}

// costPatch validates a request and turns it into the merge-patch value for spec.cost.
// Every key is present: a nil value deletes that field, so the stored config becomes
// exactly what was sent rather than a merge with whatever was there before. When nothing
// is set at all it returns nil, which removes spec.cost and falls back to the default.
func costPatch(req costSettingsRequest) (map[string]interface{}, error) {
	node := []*float64{req.NodeMonthlyCost, req.NodeVCPUs, req.NodeMemoryGiB}
	given := 0
	for _, v := range node {
		if v != nil {
			given++
			if *v <= 0 {
				return nil, fmt.Errorf("node cost, vCPUs and memory must all be positive")
			}
		}
	}
	// Half a node description silently does nothing -- rateCard needs all three to derive
	// rates -- so it is refused rather than stored and ignored.
	if given != 0 && given != len(node) {
		return nil, fmt.Errorf("give the node's monthly cost, vCPUs and memory together, or none of them")
	}
	for name, v := range map[string]*float64{
		"cpuCoreHour": req.CPUCoreHour, "memoryGiBHour": req.MemoryGiBHour, "storageGiBMonth": req.StorageGiBMonth,
	} {
		if v != nil && *v < 0 {
			return nil, fmt.Errorf("%s cannot be negative", name)
		}
	}

	out := map[string]interface{}{}
	set := false
	put := func(key string, v *float64) {
		if v == nil {
			out[key] = nil
			return
		}
		out[key] = *v
		set = true
	}
	put("nodeMonthlyCost", req.NodeMonthlyCost)
	put("nodeVCPUs", req.NodeVCPUs)
	put("nodeMemoryGiB", req.NodeMemoryGiB)
	put("cpuCoreHour", req.CPUCoreHour)
	put("memoryGiBHour", req.MemoryGiBHour)
	put("storageGiBMonth", req.StorageGiBMonth)
	if req.Currency != nil && *req.Currency != "" {
		out["currency"] = *req.Currency
		set = true
	} else {
		out["currency"] = nil
	}
	if !set {
		return nil, nil
	}
	return out, nil
}

// GetCostSettings returns the stored rate card and the rates actually in effect.
func (h *Handler) GetCostSettings(c *gin.Context) {
	stored := map[string]interface{}{}
	if cfg, err := h.K8s.GetClusterResource(c.Request.Context(), k8s.VestaConfigGVR, vestaConfigName); err == nil {
		if cost, found, _ := unstructured.NestedMap(cfg.Object, "spec", "cost"); found {
			stored = cost
		}
	}
	rates := h.rateCard(c)
	c.JSON(http.StatusOK, gin.H{
		"config":    stored,
		"effective": rates,
		"default":   services.DefaultRateCard,
		"estimated": rates == services.DefaultRateCard,
	})
}

// SetCostSettings replaces the rate card. An empty body resets it to the default.
func (h *Handler) SetCostSettings(c *gin.Context) {
	var req costSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	cost, err := costPatch(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	ctx := c.Request.Context()
	// One VestaConfig or refuse, for the same reason as the security posture: rateCard
	// reads the first one listed, so writing to one of several takes effect unpredictably.
	list, err := h.K8s.ListResources(ctx, k8s.VestaConfigGVR, "", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}
	if len(list.Items) > 1 {
		c.JSON(http.StatusConflict, models.ErrorResponse{Code: 409,
			Message: "more than one VestaConfig exists, so the rate card cannot be set safely"})
		return
	}
	if len(list.Items) == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "no VestaConfig found; install the Helm chart first"})
		return
	}

	var value interface{} = cost
	if cost == nil {
		value = nil // removes spec.cost entirely
	}
	patch, _ := json.Marshal(map[string]interface{}{"spec": map[string]interface{}{"cost": value}})
	if _, err := h.K8s.PatchClusterResource(ctx, k8s.VestaConfigGVR, list.Items[0].GetName(), patch); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	h.auditLog(c, "set_cost_rates", "platform", "cost", "", "", "", cost)

	rates := h.rateCard(c)
	c.JSON(http.StatusOK, gin.H{"status": "saved", "effective": rates, "estimated": rates == services.DefaultRateCard})
}
