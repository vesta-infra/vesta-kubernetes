The recipe (four terminals)

cd ~/workspace/vesta/vesta-kubernetes

# 1. database — the ONLY service you should use compose for (see below)
podman compose up -d postgres

# 2. operator
make run-operator

# 3. api
DATABASE_URL="postgres://vesta:vesta-dev@localhost:5433/vesta?sslmode=disable" \
JWT_SECRET=dev-secret make run-api

# 4. ui
make run-ui        # http://localhost:3000
JWT_SECRET isn't in the CONTRIBUTING snippet but you want it set — without it tokens are signed with an empty key. KUBECONFIG is unnecessary: client.go:78-91 tries in-cluster config, then falls back to ~/.kube/config, which is your current kind-local-cluster context.

# cli
./bin/vesta apps list  