package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"k8s.io/client-go/util/retry"

	"kubernetes.getvesta.sh/api/internal/k8s"
	"kubernetes.getvesta.sh/api/internal/models"
)

// Resource quotas for an environment.
//
// The API's job here is mostly to report what the operator already worked out. A quota is
// not applied the moment it is set: the operator computes what the environment has committed
// and refuses to enforce a quota below that, because a ResourceQuota does not constrain pods
// that already exist -- it refuses the next admission, so an over-tight quota fails during
// somebody's deploy rather than when it was configured.

// GetEnvironmentQuota returns the configured quota and what the operator observed.
func (h *Handler) GetEnvironmentQuota(c *gin.Context) {
	env, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaEnvironmentGVR, vestaSystemNS, c.Param("env"))
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Code: 404, Message: "environment not found"})
		return
	}

	spec, _, _ := unstructuredNestedMap(env.Object, "spec")
	status, _, _ := unstructuredNestedMap(env.Object, "status")
	quotaSpec, _, _ := unstructuredNestedMap(spec, "quota")
	quotaStatus, _, _ := unstructuredNestedMap(status, "quota")

	c.JSON(http.StatusOK, gin.H{
		"environment": c.Param("env"),
		"quota":       quotaSpec,
		// Committed, used, and whether enforcing this would already be exceeded. Computed
		// by the operator on every pass, enforced or not, so the numbers are available
		// before anyone commits to them.
		"status": quotaStatus,
	})
}

// SetEnvironmentQuota configures an environment's quota.
func (h *Handler) SetEnvironmentQuota(c *gin.Context) {
	var req struct {
		Enforce        *bool  `json:"enforce"`
		RequestsCPU    string `json:"requestsCpu"`
		RequestsMemory string `json:"requestsMemory"`
		LimitsCPU      string `json:"limitsCpu"`
		LimitsMemory   string `json:"limitsMemory"`
		StorageTotal   string `json:"storageTotal"`
		MaxPods        *int32 `json:"maxPods"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	envName := c.Param("env")

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		env, err := h.K8s.GetResource(c.Request.Context(), k8s.VestaEnvironmentGVR, vestaSystemNS, envName)
		if err != nil {
			return err
		}

		spec, _, _ := unstructuredNestedMap(env.Object, "spec")
		quota := map[string]interface{}{}

		for key, value := range map[string]string{
			"requestsCpu":    req.RequestsCPU,
			"requestsMemory": req.RequestsMemory,
			"limitsCpu":      req.LimitsCPU,
			"limitsMemory":   req.LimitsMemory,
			"storageTotal":   req.StorageTotal,
		} {
			if value != "" {
				quota[key] = value
			}
		}
		if req.MaxPods != nil {
			quota["maxPods"] = int64(*req.MaxPods)
		}
		if req.Enforce != nil {
			quota["enforce"] = *req.Enforce
		}

		if len(quota) == 0 {
			// Clearing it entirely, which the operator reads as "remove the objects".
			delete(spec, "quota")
		} else {
			spec["quota"] = quota
		}
		env.Object["spec"] = spec

		_, err = h.K8s.UpdateResource(c.Request.Context(), k8s.VestaEnvironmentGVR, vestaSystemNS, env)
		return err
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Code: 500, Message: err.Error()})
		return
	}

	h.auditLog(c, "set_environment_quota", "environment", envName, envName,
		c.Param("projectId"), envName,
		map[string]interface{}{"enforce": req.Enforce})

	c.JSON(http.StatusOK, gin.H{
		"environment": envName,
		// The operator decides whether it actually applies, and reports that in status on
		// its next pass. Saying "saved" rather than "enforced" keeps the two apart.
		"status": "saved",
		"note":   fmt.Sprintf("the operator will report on %s within a minute", envName),
	})
}
