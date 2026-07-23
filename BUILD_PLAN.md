# KubeFlow: Kubernetes Operator Build Plan

## Project Overview
A Kubernetes Operator built in Go using `controller-runtime` to manage custom `Application` CRDs. It automates Helm deployments, ensures desired state through reconciliation loops, and includes professional-grade features like admission webhooks, observability, and automated rollbacks.

## Tech Stack
| Layer | Choice | Why |
| :--- | :--- | :--- |
| **Language** | Go | Native to `controller-runtime`. |
| **Local Cluster** | `kind` | Cheap, fast, one-command setup. |
| **Scaffolding** | `kubebuilder v3` | Industry standard for CRD/Controller boilerplate. |
| **Helm Integration** | Helm Go SDK (`v3`) | Programmatic installs without shell-executing binaries. |
| **Webhook TLS** | `cert-manager` | Automated cert rotation for admission webhooks. |
| **CI/CD** | GitHub Actions | Integration with GHCR and Trivy. |
| **Security** | Trivy | Native container vulnerability scanning. |
| **Observability** | Prometheus + Grafana | ServiceMonitor integration for custom reconciliation metrics. |

---

## Phase 1: Foundation & Scaffolding
*   **Cluster Setup**: `kind create cluster --name app-operator`.
*   **Init**: `kubebuilder init --domain kubeflow.dev --repo github.com/rishabhtiwari/kubeflow`.
*   **CRD Generation**: `kubebuilder create api --group apps --version v1alpha1 --kind Application`.
*   **Spec Definition**: Define `ApplicationSpec` with:
    *   `ChartName`, `RepoURL`, `Version`.
    *   `ValuesSecretRef`: Reference to a secret containing overrides.
    *   `Namespace`: Target namespace for the app.
*   **Milestone**: `kubectl apply` accepts an `Application` YAML.

## Phase 2: Reconciliation & Helm Logic
*   **Helm SDK**: Implement `Manager` using the Helm Go SDK.
*   **Lifecycle**:
    *   **Create/Update**: Trigger `helm upgrade --install`.
    *   **Delete**: Implement **Finalizers** to clean up resources.
*   **Namespace Ownership**:
    *   Operator creates namespaces if they don't exist.
    *   Tag created namespaces with an annotation `kubeflow.dev/managed-by: operator`.
    *   Only delete namespaces carrying this annotation.
*   **Secret Management**: Map `ValuesSecretRef` into the Helm install action.
*   **Milestone**: Applying a CR triggers a successful Helm release.

## Phase 3: Admission Webhooks
*   **Validation**: Add a `ValidatingWebhook` to ensure:
    *   Version is in semver format.
    *   `ValuesSecretRef` is not empty if required.
    *   Namespace name is valid.
*   **Security**: Ensure `cert-manager` is handling the webhook certificates.
*   **Milestone**: Invalid CRDs are rejected by the API server.

## Phase 4: CI/CD & Automated Rollbacks
*   **Rollback Strategy**:
    *   **Non-blocking**: After upgrade, requeue the reconciliation after 30s.
    *   **Health Check**: Check underlying `Deployment` status.
    *   **Action**: If "Failed", trigger `helm rollback` via SDK and set status to `Degraded`.
*   **Pipeline**: GitHub Actions to build, Trivy scan, and push to GHCR.
*   **Milestone**: Automatic rollback on failed health probes; full CI pipeline < 3 mins.

## Phase 5: Observability
*   **Metrics**: Export via `prometheus/client_golang` with labels `[application_name, namespace]`.
    *   `reconciliation_latency_seconds` (Histogram).
    *   `deployment_success_total` (Counter).
    *   `active_managed_apps` (Gauge).
*   **Dashboards**: Create Grafana JSON tracking reconcile duration and error spikes.
*   **Discovery**: Configure `ServiceMonitor` for automatic Prometheus scraping.
*   **Milestone**: Real-time visualization of operator health in Grafana.

---

## Technical Constraints & Notes
*   **RBAC**: The operator uses `ClusterAdmin` (documented in README as a tradeoff for flexibility).
*   **Concurrency**: Initial implementation will use default concurrency (1 worker).
*   **Caching**: Leveraging default `controller-runtime` cache to protect the K8s API server.
