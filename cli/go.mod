module kubernetes.getvesta.sh/cli

go 1.22

require github.com/spf13/cobra v1.8.0

require go.yaml.in/yaml/v2 v2.4.2 // indirect

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	sigs.k8s.io/yaml v1.6.0
)
