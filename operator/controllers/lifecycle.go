package controllers

import (
	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

// Lifecycle state resolution.
//
// An app's desired state lives in spec.desiredState and is owned by whoever asked for the
// change; status.phase is an observation and is owned by this operator alone. Keeping the
// two apart is what makes sleep and stop work at all.
//
// They used to be conflated: the API expressed "sleep this app" by patching status.phase,
// which a resource with a status subresource silently discards. Sleep then deadlocked --
// replicas went to zero only once the phase read "Sleeping", and the phase read "Sleeping"
// only once replicas were already zero -- and stop never did anything. Reading the
// instruction from spec removes the cycle, and makes a stopped app stay stopped across the
// 30-second resync rather than reverting.

// normalizeDesiredState maps the empty value onto "running" so an app written before
// spec.desiredState existed behaves exactly as it did.
func normalizeDesiredState(s string) string {
	if s == "" {
		return vestav1alpha1.DesiredStateRunning
	}
	return s
}

// atRest reports whether a desired state means "no pods".
func atRest(desiredState string) bool {
	switch normalizeDesiredState(desiredState) {
	case vestav1alpha1.DesiredStateSleeping, vestav1alpha1.DesiredStateStopped:
		return true
	}
	return false
}

// resolveReplicas returns the replica count for an environment and whether the operator
// should write it onto the Deployment at all.
//
// The second return exists because an HPA normally owns spec.replicas: writing it on every
// reconcile would fight the autoscaler and pin the app to its floor. Resting states are the
// exception -- zero has to be written even under an HPA, or the autoscaler keeps the app
// alive and the instruction to sleep is ignored.
func resolveReplicas(desiredState string, envReplicas *int32, autoscaleEnabled bool) (int32, bool) {
	if atRest(desiredState) {
		return 0, true
	}

	replicas := int32(1)
	if envReplicas != nil {
		replicas = *envReplicas
	}
	return replicas, !autoscaleEnabled
}

// restingPhase returns the phase an app at rest should report, and whether one applies.
//
// This is consulted before any observation of the cluster, so a sleeping or stopped app
// reports why it has no pods instead of being diagnosed as "Pending" -- which is what an
// app with zero desired replicas would otherwise look like.
func restingPhase(desiredState string) (string, bool) {
	switch normalizeDesiredState(desiredState) {
	case vestav1alpha1.DesiredStateSleeping:
		return "Sleeping", true
	case vestav1alpha1.DesiredStateStopped:
		return "Stopped", true
	}
	return "", false
}
