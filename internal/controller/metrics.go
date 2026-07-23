/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	appsv1alpha1 "github.com/rishabht877/HelmWarden/api/v1alpha1"
)

const (
	labelAppName   = "application_name"
	labelNamespace = "namespace"
)

// Custom operator metrics, registered on controller-runtime's shared registry so they are exposed
// on the manager's existing /metrics endpoint alongside the built-in controller-runtime metrics.
var (
	// reconciliationLatency measures how long a full reconcile takes, per Application.
	reconciliationLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "helmwarden_reconciliation_latency_seconds",
		Help:    "Duration of Application reconciliations in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{labelAppName, labelNamespace})

	// deploymentSuccessTotal counts transitions into the Deployed (healthy) phase.
	deploymentSuccessTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "helmwarden_deployment_success_total",
		Help: "Total number of Applications that reached the Deployed phase.",
	}, []string{labelAppName, labelNamespace})

	// activeManagedApps is the current number of managed Applications, labeled by target namespace.
	// It is recomputed from the cache each reconcile so it survives controller restarts (an
	// increment/decrement counter would reset to zero and undercount existing Applications).
	activeManagedApps = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "helmwarden_active_managed_apps",
		Help: "Number of Applications currently managed, by target namespace.",
	}, []string{labelNamespace})
)

func init() {
	ctrlmetrics.Registry.MustRegister(reconciliationLatency, deploymentSuccessTotal, activeManagedApps)
}

// refreshActiveGauge recomputes the active-managed-apps gauge from the cached Application list,
// grouped by target namespace. Called once per reconcile (with a single worker, no Reset race).
func (r *ApplicationReconciler) refreshActiveGauge(ctx context.Context) {
	var list appsv1alpha1.ApplicationList
	if err := r.List(ctx, &list); err != nil {
		return // best-effort; the gauge will be refreshed on the next reconcile
	}
	counts := map[string]int{}
	for i := range list.Items {
		app := &list.Items[i]
		if !app.DeletionTimestamp.IsZero() {
			continue
		}
		counts[app.Spec.Namespace]++
	}
	activeManagedApps.Reset()
	for ns, n := range counts {
		activeManagedApps.WithLabelValues(ns).Set(float64(n))
	}
}
