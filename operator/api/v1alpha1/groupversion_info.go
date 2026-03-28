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
