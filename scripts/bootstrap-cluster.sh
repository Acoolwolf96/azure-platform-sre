#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

export KUBECONFIG="${REPO_ROOT}/.generated/aks-kubeconfig"

echo "Bootstrapping Kubernetes cluster..."

kubectl create namespace argocd \
  --dry-run=client \
  -o yaml \
  | kubectl apply -f -

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


echo "Allowing Argo CD to manage EndpointSlices..."

kubectl get configmap argocd-cm -n argocd -o json \
  | jq -r '.data["resource.exclusions"] // ""' \
  > /tmp/argocd-exclusions.yaml

python3 - <<'INNERPY'
from pathlib import Path

src = Path("/tmp/argocd-exclusions.yaml").read_text().splitlines()

out = []
skip = False

for line in src:
    if line.startswith("### Network resources created by the Kubernetes control plane"):
        skip = True
        continue

    if skip and line.startswith("### Internal Kubernetes resources"):
        skip = False
        out.append(line)
        continue

    if not skip:
        out.append(line)

Path("/tmp/argocd-exclusions-new.yaml").write_text(
    "\n".join(out).strip() + "\n"
)
INNERPY

jq -n \
  --rawfile exclusions /tmp/argocd-exclusions-new.yaml \
  '{"data":{"resource.exclusions":$exclusions}}' \
  > /tmp/argocd-cm-patch.json

kubectl patch configmap argocd-cm \
  -n argocd \
  --type merge \
  --patch-file /tmp/argocd-cm-patch.json

kubectl rollout restart \
  statefulset/argocd-application-controller \
  -n argocd

kubectl rollout status \
  statefulset/argocd-application-controller \
  -n argocd \
  --timeout=180s

echo "Bootstrapping root application..."

kubectl apply -f "${REPO_ROOT}/gitops/root-app.yaml"

echo "Cluster bootstrap complete."
