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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	appsv1alpha1 "github.com/rishabht877/HelmWarden/api/v1alpha1"
	"github.com/rishabht877/HelmWarden/internal/helm"
)

const (
	// finalizerName gates deletion so we can uninstall the Helm release and GC the namespace first.
	finalizerName = "apps.helmwarden.dev/finalizer"
	// managedByAnnotation marks namespaces the operator created, so we only ever delete our own.
	managedByAnnotation = "helmwarden.dev/managed-by"
	managedByValue      = "operator"
	// namespaceIndexKey indexes Applications by their target namespace for the shared-namespace guard.
	namespaceIndexKey = "spec.namespace"
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

// Reconcile drives the cluster toward the desired state declared by an Application: it manages a
// finalizer, ensures the target namespace exists, resolves value overrides, and installs or
// upgrades the corresponding Helm release. (Health checks and rollback are layered on in Phase 4.)
func (r *ApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var app appsv1alpha1.Application
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Deletion: run cleanup behind the finalizer.
	if !app.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &app)
	}

	// Register the finalizer before creating any external state. The Update re-triggers us; the
	// next pass finds the finalizer already present and proceeds. (Finalizer changes are metadata,
	// so they don't bump generation and won't fool the idempotency gate below.)
	if controllerutil.AddFinalizer(&app, finalizerName) {
		if err := r.Update(ctx, &app); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Idempotency gate: if we've already deployed this spec generation, do nothing. Without this,
	// our own status writes re-trigger the watch and each pass runs another Helm upgrade, inflating
	// revisions until concurrent operations collide. (Values-drift + health requeues come later.)
	if app.Status.ObservedGeneration == app.Generation && app.Status.Phase == appsv1alpha1.PhaseDeployed {
		return ctrl.Result{}, nil
	}

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

// reconcileDelete uninstalls the release, GCs the namespace if we own it, then drops the finalizer.
func (r *ApplicationReconciler) reconcileDelete(ctx context.Context, app *appsv1alpha1.Application) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(app, finalizerName) {
		return ctrl.Result{}, nil
	}

	if err := r.Helm.Uninstall(ctx, app.Name, app.Spec.Namespace); err != nil {
		return ctrl.Result{}, fmt.Errorf("uninstall release %q: %w", app.Name, err)
	}
	if err := r.maybeDeleteNamespace(ctx, app); err != nil {
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(app, finalizerName)
	if err := r.Update(ctx, app); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("finalized application", "release", app.Name, "namespace", app.Spec.Namespace)
	return ctrl.Result{}, nil
}

// ensureNamespace creates the target namespace if absent, tagging namespaces we create with the
// managed-by annotation so deletion can tell ours apart from pre-existing ones.
func (r *ApplicationReconciler) ensureNamespace(ctx context.Context, name string) error {
	var ns corev1.Namespace
	err := r.Get(ctx, client.ObjectKey{Name: name}, &ns)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	ns = corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: map[string]string{managedByAnnotation: managedByValue},
		},
	}
	if err := r.Create(ctx, &ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// maybeDeleteNamespace deletes the target namespace only if the operator created it (carries the
// managed-by annotation) and no other live Application still targets it.
func (r *ApplicationReconciler) maybeDeleteNamespace(ctx context.Context, app *appsv1alpha1.Application) error {
	var ns corev1.Namespace
	if err := r.Get(ctx, client.ObjectKey{Name: app.Spec.Namespace}, &ns); err != nil {
		return client.IgnoreNotFound(err)
	}
	if ns.Annotations[managedByAnnotation] != managedByValue {
		return nil
	}
	others, err := r.otherApplicationsInNamespace(ctx, app)
	if err != nil {
		return err
	}
	if others > 0 {
		return nil
	}
	if err := r.Delete(ctx, &ns); err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}

// otherApplicationsInNamespace counts live Applications (other than app) targeting the same namespace.
func (r *ApplicationReconciler) otherApplicationsInNamespace(ctx context.Context, app *appsv1alpha1.Application) (int, error) {
	var list appsv1alpha1.ApplicationList
	if err := r.List(ctx, &list, client.MatchingFields{namespaceIndexKey: app.Spec.Namespace}); err != nil {
		return 0, err
	}
	count := 0
	for i := range list.Items {
		other := &list.Items[i]
		if other.UID == app.UID || !other.DeletionTimestamp.IsZero() {
			continue
		}
		count++
	}
	return count, nil
}

// resolveValues loads Helm value overrides from the referenced Secret (in the Application's own
// namespace, to avoid a chicken-and-egg dependency on the target namespace).
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

// SetupWithManager sets up the controller with the Manager, registering the namespace field index.
func (r *ApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &appsv1alpha1.Application{}, namespaceIndexKey,
		func(o client.Object) []string {
			return []string{o.(*appsv1alpha1.Application).Spec.Namespace}
		}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1alpha1.Application{}).
		Named("application").
		Complete(r)
}
