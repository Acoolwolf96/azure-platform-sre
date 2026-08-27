#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

export KUBECONFIG="${REPO_ROOT}/.generated/aks-kubeconfig"

: "${POSTGRES_ADMIN_PASSWORD:?POSTGRES_ADMIN_PASSWORD is required}"

echo "Bootstrapping Kubernetes cluster..."

kubectl create namespace argocd \
  --dry-run=client \
  -o yaml \
  | kubectl apply -f -

kubectl create namespace jobs \
  --dry-run=client \
  -o yaml \
  | kubectl apply -f -

echo "Installing Argo CD..."

kubectl apply -n argocd \
  --server-side \
  --force-conflicts \
  -f https://raw.githubusercontent.com/argoproj/argo-cd/v3.5.0/manifests/install.yaml

echo "Waiting for Argo CD..."

kubectl wait \
  --for=condition=Available \
  deployment \
  --all \
  -n argocd \
  --timeout=180s

kubectl rollout status \
  statefulset/argocd-application-controller \
  -n argocd \
  --timeout=180s


echo "Ensuring Service Bus jobs queue exists..."

SERVICEBUS_URL="http://localhost:4577/sb-platform-sre-servicebus/jobs"

if curl -fsS "${SERVICEBUS_URL}" >/dev/null 2>&1; then
  echo "Service Bus queue jobs already exists."
else
  curl -fsS \
    -X PUT \
    "${SERVICEBUS_URL}" \
    -H "Content-Type: application/atom+xml;type=entry;charset=utf-8" \
    --data-binary @- >/dev/null <<'XML'
<?xml version="1.0" encoding="utf-8"?>
<entry xmlns="http://www.w3.org/2005/Atom">
  <content type="application/xml">
    <QueueDescription xmlns="http://schemas.microsoft.com/netservices/2010/10/servicebus/connect" />
  </content>
</entry>
XML

  echo "Service Bus queue jobs created."
fi


echo "Discovering Docker network..."

FLOCI_NETWORK="$(
  docker inspect floci-az \
    --format '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{"\n"}}{{end}}' \
    | head -n1
)"

DOCKER_GW="$(
  docker network inspect "${FLOCI_NETWORK}" \
    --format '{{(index .IPAM.Config 0).Gateway}}'
)"

echo "Docker network: ${FLOCI_NETWORK}"
echo "Docker gateway: ${DOCKER_GW}"


echo "Waiting for Service Bus broker..."

SB_HOST_PORT=""

for i in $(seq 1 30); do
  SB_HOST_PORT="$(
    docker port floci-az-servicebus-default 5672/tcp 2>/dev/null \
      | awk -F: '/0.0.0.0/ {print $2; exit}'
  )"

  if [ -n "${SB_HOST_PORT}" ]; then
    break
  fi

  sleep 2
done

if [ -z "${SB_HOST_PORT}" ]; then
  echo "Could not determine Service Bus host port." >&2
  exit 1
fi

echo "Service Bus host port: ${SB_HOST_PORT}"


echo "Waiting for PostgreSQL..."

PG_CONTAINER="floci-az-pg-pg-platform-sre"
PG_HOST_PORT=""

for i in $(seq 1 30); do
  PG_HOST_PORT="$(
    docker port "${PG_CONTAINER}" 5432/tcp 2>/dev/null \
      | awk -F: '/0.0.0.0/ {print $2; exit}'
  )"

  if [ -n "${PG_HOST_PORT}" ]; then
    break
  fi

  sleep 2
done

if [ -z "${PG_HOST_PORT}" ]; then
  echo "Could not determine PostgreSQL host port." >&2
  exit 1
fi

echo "PostgreSQL host port: ${PG_HOST_PORT}"


echo "Ensuring jobsdb exists..."

if ! docker exec \
  -e PGPASSWORD="${POSTGRES_ADMIN_PASSWORD}" \
  "${PG_CONTAINER}" \
  psql \
    -h 127.0.0.1 \
    -U platformadmin \
    -d postgres \
    -tAc "SELECT 1 FROM pg_database WHERE datname='jobsdb';" \
    | grep -qx 1
then
  docker exec \
    -e PGPASSWORD="${POSTGRES_ADMIN_PASSWORD}" \
    "${PG_CONTAINER}" \
    createdb \
      -h 127.0.0.1 \
      -U platformadmin \
      jobsdb

  echo "Database jobsdb created."
else
  echo "Database jobsdb already exists."
fi


echo "Applying database migrations..."

for migration in "${REPO_ROOT}"/applications/jobs-api/migrations/*.sql; do
  echo "Applying $(basename "${migration}")..."

  docker exec \
    -i \
    -e PGPASSWORD="${POSTGRES_ADMIN_PASSWORD}" \
    "${PG_CONTAINER}" \
    psql \
      -h 127.0.0.1 \
      -U platformadmin \
      -d jobsdb \
    < "${migration}"
done


echo "Creating PostgreSQL Kubernetes credentials..."

kubectl create secret generic postgres-credentials \
  -n jobs \
  --from-literal=PGUSER=platformadmin \
  --from-literal=PGPASSWORD="${POSTGRES_ADMIN_PASSWORD}" \
  --from-literal=PGDATABASE=jobsdb \
  --dry-run=client \
  -o yaml \
  | kubectl apply -f -


echo "Creating Service Bus runtime endpoint..."

cat <<SBEOF | kubectl apply -f -
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata:
  name: servicebus
  namespace: jobs
  labels:
    kubernetes.io/service-name: servicebus
addressType: IPv4
ports:
  - name: amqp
    protocol: TCP
    port: ${SB_HOST_PORT}
endpoints:
  - addresses:
      - "${DOCKER_GW}"
SBEOF


echo "Ensuring Blob Storage results container exists..."

BLOB_STATUS="$(
  curl -sS \
    -o /dev/null \
    -w '%{http_code}' \
    -X PUT \
    "http://localhost:4577/devstoreaccount1/job-results?restype=container" \
    -H "x-ms-version: 2023-11-03" \
    -H "x-ms-date: $(date -u -R)" \
    -H "Authorization: SharedKey devstoreaccount1:development"
)"

if [ "${BLOB_STATUS}" = "201" ] || [ "${BLOB_STATUS}" = "409" ]; then
  echo "Blob container job-results is available."
else
  echo "Blob container creation failed with HTTP ${BLOB_STATUS}." >&2
  exit 1
fi


echo "Creating Blob Storage runtime endpoint..."

cat <<BLOBEOF | kubectl apply -f -
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata:
  name: blob-storage
  namespace: jobs
  labels:
    kubernetes.io/service-name: blob-storage
addressType: IPv4
ports:
  - name: http
    protocol: TCP
    port: 4577
endpoints:
  - addresses:
      - "${DOCKER_GW}"
BLOBEOF


echo "Creating PostgreSQL runtime endpoint..."

cat <<PGEOF | kubectl apply -f -
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata:
  name: postgres
  namespace: jobs
  labels:
    kubernetes.io/service-name: postgres
addressType: IPv4
ports:
  - name: postgresql
    protocol: TCP
    port: ${PG_HOST_PORT}
endpoints:
  - addresses:
      - "${DOCKER_GW}"
PGEOF


echo "Bootstrapping root Argo CD application..."

kubectl apply -f "${REPO_ROOT}/gitops/root-app.yaml"

echo
echo "Cluster bootstrap complete."
