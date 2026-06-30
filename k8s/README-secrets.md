# Secret Management

Do not commit real credentials to this repository.

1. Generate strong passwords:
   ```bash
   openssl rand -base64 32
   ```
2. Apply secrets via `kubectl` from a local, non-committed file:
   ```bash
   kubectl apply -f k8s/secrets.local.yaml
   ```
3. If any committed secret was ever used in a real cluster, rotate it immediately in the database and in all downstream consumers.

For production, use Sealed Secrets, external-secrets, or SOPS instead of plain manifests.
