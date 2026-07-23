# HelmWarden

A Kubernetes operator that turns Helm releases into declarative, self-healing custom resources.

Declare an `Application` — a chart, a repo, a version, a target namespace — and HelmWarden reconciles the cluster to match: installing or upgrading the Helm release, creating and owning the target namespace, watching the rolled-out workloads for health, and automatically rolling back to the last good revision when a release goes unhealthy.

Built with [kubebuilder](https://book.kubebuilder.io/) v4 / controller-runtime and the Helm Go SDK.

## Why

`helm upgrade` is imperative and stateless — nothing reconciles drift, nothing rolls back a bad release on its own, and there's no CRD-native way to express "this chart should be running, at this version, here." HelmWarden closes that gap: a Helm release becomes a Kubernetes object with a spec, a status, conditions, and a controller that continuously drives toward desired state.

## The `Application` API

```yaml
apiVersion: apps.helmwarden.dev/v1alpha1
kind: Application
metadata:
  name: podinfo
spec:
  chartName: podinfo
  repoURL: https://stefanprodan.github.io/podinfo
  version: 6.7.1              # must be valid semver (enforced by the admission webhook)
  namespace: demo            # created + owned by the operator if absent
  valuesSecretRef:           # optional Helm value overrides
    name: podinfo-values
    key: values.yaml         # defaults to "values.yaml"
  progressDeadlineSeconds: 300
```

`status` reports a `phase` (`Pending` → `Deploying` → `Deployed` / `Degraded` / `Failed`), standard `conditions` (`Released`, `Healthy`, `Ready`), the live `helmRevision`, and bookkeeping used for idempotent reconciles and rollback anti-thrash.

## Quickstart (local, kind)

```sh
# 1. Cluster + CRDs
kind create cluster --name app-operator
make install

# 2. Run the controller against the cluster
make run

# 3. Apply a sample Application
kubectl apply -f config/samples/apps_v1alpha1_application.yaml
kubectl get applications
```

## Status / roadmap

- [x] **Phase 1** — CRD + scaffolding (`Application` types, status subresource, printer columns)
- [ ] **Phase 2** — Helm reconciliation (install/upgrade, finalizer cleanup, namespace ownership, idempotency)
- [ ] **Phase 3** — validating admission webhook (semver / values / namespace), cert-manager TLS
- [ ] **Phase 4** — health-based automated rollback + CI (Trivy, GHCR)
- [ ] **Phase 5** — Prometheus metrics + Grafana dashboard (ServiceMonitor)

The original phased design lives in [`BUILD_PLAN.md`](./BUILD_PLAN.md); this README tracks the current state.
