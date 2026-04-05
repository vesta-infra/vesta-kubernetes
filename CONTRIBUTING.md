# Contributing to Vesta

Vesta is open-source under GPL-3.0 and we welcome contributions of all kinds — bug fixes, features, docs, and feedback.

## Getting Started

### Prerequisites

- Go 1.22+
- Node.js 20+
- Docker & Docker Compose
- A Kubernetes cluster (local or remote)
- Helm 3

### Clone the Repository

```bash
git clone https://github.com/vesta-infra/vesta-kubernetes.git
cd vesta-kubernetes
```

### Start the Database

```bash
docker compose up postgres
```

### Run the Components

Each component runs independently during development:

```bash
# Operator
cd operator && go run .

# API server
DATABASE_URL="postgres://vesta:vesta-dev@localhost:5433/vesta?sslmode=disable" make run-api

# UI
cd ui && npm install && npm run dev

# CLI
cd cli && go build -o vesta . && ./vesta --help
```

## Project Structure

```
vesta-kubernetes/
├── operator/          # Kubernetes operator (Go/Kubebuilder)
├── api/               # REST API server (Go/Gin)
├── ui/                # Web dashboard (React/TypeScript/Tailwind)
├── cli/               # CLI tool (Go/Cobra)
└── deploy/helm/vesta/ # Helm chart
```

## Development Workflow

1. Fork the repository and create a feature branch from `develop`
2. Make your changes with clear, descriptive commits
3. Ensure existing tests pass and add tests for new functionality
4. Open a pull request against the `develop` branch

## Code Style

- **Go** — Follow standard `gofmt` formatting. Run `go vet` before committing.
- **TypeScript/React** — Follow the existing Tailwind + component patterns in `ui/src`.
- **Commits** — Use conventional commit messages: `feat:`, `fix:`, `docs:`, `chore:`.

## Reporting Issues

Found a bug or have a feature request? [Open an issue](https://github.com/vesta-infra/vesta-kubernetes/issues/new) with:

- A clear title and description
- Steps to reproduce (for bugs)
- Expected vs actual behavior
- Kubernetes and Vesta version info

## Areas to Contribute

- Bug fixes and stability improvements
- New notification channels
- CLI enhancements
- Documentation improvements
- UI/UX improvements
- Helm chart enhancements
- Test coverage
