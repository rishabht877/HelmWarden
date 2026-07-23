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
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/cli-utils/pkg/kstatus/status"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	appsv1alpha1 "github.com/rishabht877/HelmWarden/api/v1alpha1"
)

// workloadKinds are the resource kinds with a meaningful notion of "ready". A Helm release may
// contain none, one, or many of these (plus Services, ConfigMaps, etc. we ignore for health).
var workloadKinds = map[string]bool{
	"Deployment":  true,
	"StatefulSet": true,
	"DaemonSet":   true,
	"Job":         true,
}

// workloadsFromManifest parses a rendered Helm manifest and returns just its workload objects.
func workloadsFromManifest(manifest string) ([]*unstructured.Unstructured, error) {
	dec := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
	var workloads []*unstructured.Unstructured
	for {
		u := &unstructured.Unstructured{}
		if err := dec.Decode(u); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if len(u.Object) == 0 {
			continue // empty document between --- separators
		}
		if workloadKinds[u.GetKind()] {
			workloads = append(workloads, u)
		}
	}
	return workloads, nil
}

// releaseHealth computes an aggregate kstatus over a release's workloads by reading each live object
// and running the same readiness engine (kstatus) that Flux/Argo use. Aggregation rules: any Failed
// workload makes the whole release Failed; otherwise any not-yet-Current workload makes it
// InProgress; only when every workload is Current is the release Current.
func (r *ApplicationReconciler) releaseHealth(ctx context.Context, manifest, defaultNamespace string) (status.Status, string, error) {
	workloads, err := workloadsFromManifest(manifest)
	if err != nil {
		return status.UnknownStatus, "", fmt.Errorf("parse release manifest: %w", err)
	}
	if len(workloads) == 0 {
		return status.CurrentStatus, "no workloads to check", nil
	}

	aggregate := status.CurrentStatus
	detail := "all workloads current"
	for _, w := range workloads {
		ns := w.GetNamespace()
		if ns == "" {
			ns = defaultNamespace
		}
		ref := w.GetKind() + "/" + w.GetName()

		live := &unstructured.Unstructured{}
		live.SetGroupVersionKind(w.GroupVersionKind())
		if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: w.GetName()}, live); err != nil {
			if apierrors.IsNotFound(err) {
				aggregate, detail = status.InProgressStatus, ref+" not created yet"
				continue
			}
			return status.UnknownStatus, "", err
		}

		res, err := status.Compute(live)
		if err != nil {
			return status.UnknownStatus, "", fmt.Errorf("compute status for %s: %w", ref, err)
		}
		switch res.Status {
		case status.FailedStatus:
			// A single failed workload fails the release outright.
			return status.FailedStatus, ref + ": " + res.Message, nil
		case status.CurrentStatus:
			// healthy; keep looking
		default:
			aggregate, detail = status.InProgressStatus, ref+": "+res.Message
		}
	}
	return aggregate, detail, nil
}

// deadlineExceeded reports whether a rollout has been progressing longer than the Application's
// progress deadline, so a stuck (never-Failed but never-Ready) release is eventually acted on.
func deadlineExceeded(app *appsv1alpha1.Application) bool {
	if app.Status.LastDeployStartTime == nil {
		return false
	}
	deadline := time.Duration(app.Spec.ProgressDeadlineSeconds) * time.Second
	return time.Since(app.Status.LastDeployStartTime.Time) > deadline
}

// handleUnhealthy responds to a failed or deadline-exceeded rollout. If there's a previous good
// revision and we haven't already rolled back for this spec, it triggers a Helm rollback and marks
// the Application Degraded; otherwise it just surfaces Degraded and stops (anti-thrash: at most one
// rollback per spec generation — LastFailedRevision is reset only when the spec/values change).
func (r *ApplicationReconciler) handleUnhealthy(ctx context.Context, app *appsv1alpha1.Application, health status.Status, detail string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	reason := "WorkloadFailed"
	if health != status.FailedStatus {
		reason = "ProgressDeadlineExceeded"
		detail = fmt.Sprintf("workloads not ready within %ds", app.Spec.ProgressDeadlineSeconds)
	}
	failedRevision := app.Status.HelmRevision

	switch {
	case app.Status.LastFailedRevision != 0:
		// Already rolled back once for this spec and it's still unhealthy — stop to avoid thrash.
		// Terminal for this spec generation, so poll slowly.
		return r.setDegraded(ctx, app, reason, detail+" (already rolled back; awaiting a spec fix)", steadyPollInterval)
	case failedRevision <= 1:
		// First install exceeded its deadline and there's no previous revision to fall back to. It
		// may still be a slow (not stuck) rollout, so keep polling fast in case it recovers.
		return r.setDegraded(ctx, app, reason, detail+" (no previous revision to roll back to)", healthPollInterval)
	}

	log.Info("release unhealthy; rolling back", "revision", failedRevision, "reason", reason)
	app.Status.Phase = appsv1alpha1.PhaseRollingBack
	app.Status.LastFailedRevision = failedRevision
	setCondition(app, appsv1alpha1.ConditionHealthy, metav1.ConditionFalse, reason, detail)
	setCondition(app, appsv1alpha1.ConditionReady, metav1.ConditionFalse, "RollingBack",
		fmt.Sprintf("revision %d unhealthy; rolling back", failedRevision))
	if err := r.Status().Update(ctx, app); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.Helm.Rollback(ctx, app.Name, app.Spec.Namespace); err != nil {
		return r.setDegraded(ctx, app, "RollbackFailed", err.Error(), healthPollInterval)
	}
	// Rollback creates a new revision; reflect it in status.
	if rel, err := r.Helm.Get(ctx, app.Name, app.Spec.Namespace); err == nil {
		app.Status.HelmRevision = rel.Revision
	}
	// Rolled back to the previous good revision — terminal for this spec until the user fixes it.
	return r.setDegraded(ctx, app, "RolledBack",
		fmt.Sprintf("revision %d failed health checks; rolled back to the previous revision", failedRevision), steadyPollInterval)
}

// setDegraded records a Degraded status (only writing when it actually changed) and requeues at the
// given cadence so the operator keeps observing without churning the object.
func (r *ApplicationReconciler) setDegraded(ctx context.Context, app *appsv1alpha1.Application, reason, msg string, requeueAfter time.Duration) (ctrl.Result, error) {
	before := app.Status.DeepCopy()
	app.Status.Phase = appsv1alpha1.PhaseDegraded
	setCondition(app, appsv1alpha1.ConditionHealthy, metav1.ConditionFalse, reason, msg)
	setCondition(app, appsv1alpha1.ConditionReady, metav1.ConditionFalse, "Degraded", msg)
	if !reflect.DeepEqual(before, &app.Status) {
		if err := r.Status().Update(ctx, app); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}
