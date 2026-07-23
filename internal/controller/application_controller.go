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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	appsv1alpha1 "github.com/rishabht877/HelmWarden/api/v1alpha1"
	"github.com/rishabht877/HelmWarden/internal/helm"
)

// ApplicationReconciler reconciles an Application object into a managed Helm release.
type ApplicationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Helm   *helm.Manager
}

// +kubebuilder:rbac:groups=apps.helmwarden.dev,resources=applications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.helmwarden.dev,resources=applications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.helmwarden.dev,resources=applications/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile drives the cluster toward the desired state declared by an Application:
// it ensures the target namespace exists, resolves any values overrides, and installs
// or upgrades the corresponding Helm release. (Deletion, health checks, and rollback
// are layered on in later phases.)
func (r *ApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var app appsv1alpha1.Application
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Idempotency gate: if we've already deployed this spec generation, do nothing. Without
	// this, our own status writes re-trigger the watch and each pass runs another Helm upgrade,
	// inflating revisions until concurrent operations collide. (Values-drift and health-driven
	// requeues are layered on in later phases.)
	if app.Status.ObservedGeneration == app.Generation && app.Status.Phase == appsv1alpha1.PhaseDeployed {
		return ctrl.Result{}, nil
	}

	// Ensure the target namespace exists before Helm writes its release state into it.
	if err := r.ensureNamespace(ctx, app.Spec.Namespace); err != nil {
		return r.fail(ctx, &app, "NamespaceError", fmt.Errorf("ensure namespace %q: %w", app.Spec.Namespace, err))
	}

	values, err := r.resolveValues(ctx, &app)
	if err != nil {
		return r.fail(ctx, &app, "ValuesError", err)
	}

	log.Info("reconciling release", "chart", app.Spec.ChartName, "version", app.Spec.Version, "namespace", app.Spec.Namespace)
	res, err := r.Helm.InstallOrUpgrade(ctx, helm.ReleaseSpec{
		ReleaseName: app.Name,
		ChartName:   app.Spec.ChartName,
		RepoURL:     app.Spec.RepoURL,
		Version:     app.Spec.Version,
		Namespace:   app.Spec.Namespace,
		Values:      values,
	})
	if err != nil {
		return r.fail(ctx, &app, "HelmError", err)
	}

	app.Status.HelmReleaseName = res.Name
	app.Status.HelmRevision = res.Revision
	app.Status.ObservedGeneration = app.Generation
	app.Status.Phase = appsv1alpha1.PhaseDeployed
	setCondition(&app, appsv1alpha1.ConditionReleased, metav1.ConditionTrue, "ReleaseApplied",
		fmt.Sprintf("release %q at revision %d (%s)", res.Name, res.Revision, res.Status))
	setCondition(&app, appsv1alpha1.ConditionReady, metav1.ConditionTrue, "Deployed", "release deployed")
	if err := r.Status().Update(ctx, &app); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("release applied", "release", res.Name, "revision", res.Revision, "status", res.Status)
	return ctrl.Result{}, nil
}

// ensureNamespace creates the target namespace if it does not already exist.
func (r *ApplicationReconciler) ensureNamespace(ctx context.Context, name string) error {
	var ns corev1.Namespace
	err := r.Get(ctx, client.ObjectKey{Name: name}, &ns)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	ns = corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := r.Create(ctx, &ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// resolveValues loads Helm value overrides from the referenced Secret (in the Application's
// own namespace, to avoid a chicken-and-egg dependency on the target namespace).
func (r *ApplicationReconciler) resolveValues(ctx context.Context, app *appsv1alpha1.Application) (map[string]any, error) {
	ref := app.Spec.ValuesSecretRef
	if ref == nil {
		return nil, nil
	}
	key := ref.Key
	if key == "" {
		key = "values.yaml"
	}
	var secret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: app.Namespace, Name: ref.Name}, &secret); err != nil {
		return nil, fmt.Errorf("get values secret %q: %w", ref.Name, err)
	}
	raw, ok := secret.Data[key]
	if !ok {
		return nil, fmt.Errorf("values secret %q has no key %q", ref.Name, key)
	}
	return helm.ParseValues(raw)
}

// fail records a failure on status and returns the causing error so the request is requeued.
func (r *ApplicationReconciler) fail(ctx context.Context, app *appsv1alpha1.Application, reason string, cause error) (ctrl.Result, error) {
	app.Status.Phase = appsv1alpha1.PhaseFailed
	setCondition(app, appsv1alpha1.ConditionReleased, metav1.ConditionFalse, reason, cause.Error())
	setCondition(app, appsv1alpha1.ConditionReady, metav1.ConditionFalse, reason, cause.Error())
	if uerr := r.Status().Update(ctx, app); uerr != nil {
		return ctrl.Result{}, uerr
	}
	return ctrl.Result{}, cause
}

// setCondition upserts a status condition, stamping it with the current generation.
func setCondition(app *appsv1alpha1.Application, condType string, status metav1.ConditionStatus, reason, msg string) {
	apimeta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: app.Generation,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *ApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1alpha1.Application{}).
		Named("application").
		Complete(r)
}
