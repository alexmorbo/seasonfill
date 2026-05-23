#!/usr/bin/env bash
# scripts/helm-test.sh
#
# Local mirror of .github/workflows/helm.yml. Run from repo root.
# Requires helm 3.16+ and (optionally) kubeconform 0.6+ on PATH.

set -euo pipefail

CHART="deploy/helm/seasonfill"
VALUES="${CHART}/ci/values-test.yaml"
DEV_VALUES="${CHART}/ci/values-dev.yaml"

if [ ! -d "$CHART" ]; then
  echo "Run from repo root (no ${CHART} here)."
  exit 1
fi

echo "==> helm lint"
helm lint "$CHART" -f "$VALUES"

echo "==> helm template (prod-like)"
mkdir -p /tmp/seasonfill-helm-test
helm template seasonfill-test "$CHART" -f "$VALUES" \
  > /tmp/seasonfill-helm-test/all.yaml

echo "==> helm template (dev path)"
helm template seasonfill-test "$CHART" -f "$DEV_VALUES" \
  > /dev/null

echo "==> Schema negative: missing existingSecret AND apiKey"
if helm template seasonfill-test "$CHART" -f "$DEV_VALUES" \
    --set 'secrets.existingSecret=' \
    --set 'secrets.apiKey=' \
    2>/dev/null; then
  echo "FAIL: schema accepted empty secrets"
  exit 1
fi

echo "==> Schema negative: webPassword AND webPasswordHash both set"
if helm template seasonfill-test "$CHART" -f "$DEV_VALUES" \
    --set 'secrets.webPasswordHash=$2b$10$abc' \
    2>/dev/null; then
  echo "FAIL: schema accepted webPassword+webPasswordHash"
  exit 1
fi

echo "==> Schema negative: instances[].name invalid pattern"
if helm template seasonfill-test "$CHART" -f "$DEV_VALUES" \
    --set 'instances[0].name=-bad' \
    2>/dev/null; then
  echo "FAIL: schema accepted invalid instance name"
  exit 1
fi

if command -v kubeconform >/dev/null 2>&1; then
  echo "==> kubeconform"
  kubeconform \
    -strict -summary \
    -kubernetes-version 1.28.0 \
    -schema-location default \
    -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
    /tmp/seasonfill-helm-test/all.yaml
else
  echo "==> kubeconform not installed; skipping (install: https://github.com/yannh/kubeconform)"
fi

echo
echo "All checks passed."
