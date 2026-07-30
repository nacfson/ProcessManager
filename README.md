# Process Manager

A small Kubernetes-based web process manager. It provides a web dashboard to register container images and exposes them as `https://{name}.nacfson.cloud`.

## Architecture

- **Control plane**: `zlUbuntu` (x86_64)
- **Worker**: `rlLocal` (ARM64 Raspberry Pi) + future workers
- **Ingress**: `ingress-nginx` on `zlUbuntu`
- **TLS**: `cert-manager` + Let's Encrypt HTTP-01 (public) / self-signed (local)
- **Dashboard**: Go + HTMX, running inside the cluster
- **Auth**: central auth service in `nacfson-auth`, backed by Postgres in `nacfson-auth`
- **Shared app DB**: Postgres in `nacfson-data` for per-project isolated databases

## Project Layout

```
ProcessManager/
├── cmd/
│   ├── manager              # Dashboard entrypoint
│   └── auth                 # Central auth service entrypoint
├── internal/
│   ├── auth/                # Auth handlers, sessions, store, middleware
│   ├── db/                  # Postgres pool and per-project DB provisioner
│   ├── k8s/                 # Kubernetes client wrappers
│   ├── models/              # Process and auth models
│   └── handlers/            # Dashboard HTTP handlers
├── web/templates            # HTMX HTML templates
├── k8s/
│   ├── auth/                # Auth service, auth DB, ingress
│   ├── data/                # Shared app DB
│   ├── manager/             # Manager Deployment/RBAC/Ingress
│   ├── project-pn/          # Project PN manifests
│   ├── portfolio-web/       # Portfolio Web manifests
│   ├── registry/            # Optional local insecure registry
│   └── debug/               # One-off cluster helpers
└── scripts/                 # Cluster bootstrap and deploy scripts
```

## Local Development

```bash
go run ./cmd/manager
```

The dashboard expects either:
- A valid `~/.kube/config` pointing at a cluster, or
- To be run inside a pod with a ServiceAccount.

## Build & Push Image

```bash
# Build and push to GitHub Container Registry
echo "$GHCR_TOKEN" | docker login ghcr.io -u USERNAME --password-stdin
docker buildx build --platform linux/amd64 -t ghcr.io/nacfson/processmanager:latest --push .
```

Deployments pull `ghcr.io/nacfson/processmanager:latest` (auth-service and manager).
## Set Up the Cluster

### 1. Prepare both servers

On `zlUbuntu` (as root or with sudo):

```bash
./scripts/bootstrap-common.sh
```

On `rlLocal` (as root or with sudo):

```bash
./scripts/bootstrap-common.sh
```

### 2. Initialize control plane on `zlUbuntu`

```bash
CONTROL_PLANE_IP=192.168.219.100 ./scripts/bootstrap-control-plane.sh
```

Save the join command printed at the end.

### 3. Join `rlLocal` as worker

```bash
JOIN_COMMAND='kubeadm join ...' ./scripts/bootstrap-worker.sh
```

### 4. Install addons

From a machine with `kubectl` configured:

```bash
./scripts/install-addons.sh
kubectl apply -f k8s/cluster/clusterissuer.yaml
```

> Update `k8s/cluster/clusterissuer.yaml` with your email before applying.

### 5. Deploy the central auth stack

```bash
./scripts/deploy-auth.sh
```

This creates:
- `nacfson-auth` namespace with auth Postgres and auth service
- `nacfson-data` namespace with shared app Postgres
- Updated manager RBAC (ClusterRole) and auth DB secret

### 6. Register existing projects and enable auth

```bash
./scripts/register-existing-projects.sh
./scripts/enable-auth-on-projects.sh
```

### 7. Access

Add to your local `/etc/hosts`:

```text
192.168.219.100 manager.nacfson.cloud auth.nacfson.cloud pn.nacfson.cloud portfolio.nacfson.cloud
```

Sign in at `https://auth.nacfson.cloud/login` by entering the bootstrap admin email from `k8s/auth/bootstrap-secret.yaml`. A one-time magic link is emailed (or shown on-screen when SMTP is not configured).

Then access:
- `https://manager.nacfson.cloud`
- `https://pn.nacfson.cloud`
- `https://portfolio.nacfson.cloud`

> **Magic-link login requires a working SMTP relay.** Configure `k8s/auth/smtp-secret.yaml` (`SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASS`, `SMTP_FROM`) before production use. Without SMTP, the auth service prints the sign-in link to its logs and surfaces it on the link-sent page for local development.
## Database Persistence

All Postgres databases use Kubernetes PVCs provisioned by `local-path-provisioner`:

| Database | Namespace | PVC | Size | Purpose |
|----------|-----------|-----|------|---------|
| auth_db | `nacfson-auth` | `data-auth-db-0` | 2Gi | Users, sessions, projects, memberships |
| shared app DB | `nacfson-data` | `data-shared-app-db-0` | 10Gi | One DB/role per project created by the manager |

Because `local-path` stores data on the node disk (`/opt/local-path-provisioner/...`), all persistent data is tied to `zlUbuntu`. There is currently no replication or automated backup.

## Registering a Process

Processes are scoped to a project and created in the project's configured namespace. From the dashboard:

1. Go to **Projects** and create a project (e.g., name `portfolio`, namespace `portfolio-web`).
2. Open the project's process list at `/projects/portfolio/processes`.
3. Click **New Process** and fill in the form:

| Field | Example |
|-------|---------|
| Name | `portfolio` |
| Image | `ghcr.io/nacfson/portfolioweb:latest` |
| Port | `8080` |
| Replicas | `1` |
| Arch | `arm64` (for `rlLocal`) or `amd64` (for `zlUbuntu`) |
| Memory Limit | `256Mi` |

The manager creates a Deployment, Service, and Ingress with TLS and auth annotations in the project's namespace.

The dashboard no longer uses a single global `-namespace` flag. Each project's `namespace` field in the auth DB determines where its processes run.

## Adding a Worker Later

1. Run `bootstrap-common.sh` on the new server.
2. Generate a fresh join token on the control plane:
   ```bash
   kubeadm token create --print-join-command
   ```
3. Run the join command on the new worker.

## Notes

- `rlLocal` has only 905 MB RAM, so only lightweight pods should run there.
- Use `nodeSelector: kubernetes.io/arch: arm64` to pin workloads to `rlLocal`.
- Build multi-arch images when a service must run on both architectures.
- The central auth service is a single point of failure; plan to run 2+ replicas for production use.
