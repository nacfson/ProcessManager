#!/usr/bin/env bash
set -euo pipefail

# Registers the existing project-pn and portfolio-web projects in the auth DB
# and grants the bootstrapped admin user owner access to both.

DB_POD=$(kubectl get pod -n nacfson-auth -l app.kubernetes.io/name=auth-db -o jsonpath='{.items[0].metadata.name}')

psql_exec() {
  kubectl exec -n nacfson-auth "${DB_POD}" -- psql -U auth_user -d auth_db -tAc "$1"
}

echo "==> Registering existing projects"

psql_exec "
INSERT INTO projects (id, name, namespace, domain_host, type, db_required, auth_enabled)
VALUES
  (gen_random_uuid(), 'pn', 'project-pn', 'pn.nacfson.cloud', 'advanced', true, true),
  (gen_random_uuid(), 'portfolio', 'portfolio-web', 'portfolio.nacfson.cloud', 'advanced', false, true)
ON CONFLICT (name) DO UPDATE SET
  namespace = EXCLUDED.namespace,
  domain_host = EXCLUDED.domain_host,
  type = EXCLUDED.type,
  db_required = EXCLUDED.db_required,
  auth_enabled = EXCLUDED.auth_enabled;
"

ADMIN_ID=$(psql_exec "SELECT id FROM users WHERE is_admin = true ORDER BY created_at LIMIT 1;")
if [ -z "${ADMIN_ID}" ]; then
  echo "ERROR: no admin user found. Make sure auth-service has bootstrapped." >&2
  exit 1
fi

# Validate ADMIN_ID is a UUID to prevent SQL injection
if [[ ! "${ADMIN_ID}" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]]; then
  echo "ERROR: ADMIN_ID is not a valid UUID: ${ADMIN_ID}" >&2
  exit 1
fi

echo "==> Granting admin ${ADMIN_ID} owner access to projects"

psql_exec "
INSERT INTO project_members (project_id, user_id, role)
SELECT id, '${ADMIN_ID}', 'owner' FROM projects
WHERE name IN ('pn', 'portfolio')
ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role;
"

echo "==> Done. Existing projects are registered and admin has owner access."
