// Package v1alpha1 holds the Vesta API types.
//
// The groupName marker is what controller-gen reads to stamp the CRD group. Without it
// generation emits `group: ""`, which is how the committed manifests came to be stale:
// `make generate` produced unusable output, so nobody re-ran it.
//
// +kubebuilder:object:generate=true
// +groupName=kubernetes.getvesta.sh
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	GroupVersion = schema.GroupVersion{Group: "kubernetes.getvesta.sh", Version: "v1alpha1"}

	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	AddToScheme = SchemeBuilder.AddToScheme
)

func init() {
	SchemeBuilder.Register(
		&VestaApp{}, &VestaAppList{},
		&VestaProject{}, &VestaProjectList{},
		&VestaEnvironment{}, &VestaEnvironmentList{},
		&VestaConfig{}, &VestaConfigList{},
		&VestaSecret{}, &VestaSecretList{},
	)
}
