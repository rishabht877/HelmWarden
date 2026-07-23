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

package v1alpha1

import (
	"context"
	"fmt"

	"github.com/Masterminds/semver/v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	appsv1alpha1 "github.com/rishabht877/HelmWarden/api/v1alpha1"
)

// applicationlog is for logging in this package.
var applicationlog = logf.Log.WithName("application-resource")

// SetupApplicationWebhookWithManager registers the validating webhook for Application.
func SetupApplicationWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &appsv1alpha1.Application{}).
		WithValidator(&ApplicationCustomValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-apps-helmwarden-dev-v1alpha1-application,mutating=false,failurePolicy=fail,sideEffects=None,groups=apps.helmwarden.dev,resources=applications,verbs=create;update,versions=v1alpha1,name=vapplication-v1alpha1.kb.io,admissionReviewVersions=v1

// ApplicationCustomValidator validates Application resources on create and update. It rejects
// malformed specs at admission time — before a bad chart version or namespace can reach the
// reconciler and produce a failed Helm release.
type ApplicationCustomValidator struct{}

// ValidateCreate validates a newly created Application.
func (v *ApplicationCustomValidator) ValidateCreate(_ context.Context, obj *appsv1alpha1.Application) (admission.Warnings, error) {
	applicationlog.Info("validating create", "name", obj.GetName())
	return nil, validateApplication(obj)
}

// ValidateUpdate validates an updated Application.
func (v *ApplicationCustomValidator) ValidateUpdate(_ context.Context, _, newObj *appsv1alpha1.Application) (admission.Warnings, error) {
	applicationlog.Info("validating update", "name", newObj.GetName())
	return nil, validateApplication(newObj)
}

// ValidateDelete performs no validation; deletion is always allowed and cleanup runs via finalizer.
func (v *ApplicationCustomValidator) ValidateDelete(_ context.Context, _ *appsv1alpha1.Application) (admission.Warnings, error) {
	return nil, nil
}

// validateApplication aggregates all spec checks into a single structured API error so users see
// every problem at once (rather than one-at-a-time).
func validateApplication(app *appsv1alpha1.Application) error {
	var errs field.ErrorList
	spec := field.NewPath("spec")

	// version must be valid semver — this is what Helm's ChartPathOptions.Version expects; a bad
	// version otherwise fails deep inside the reconciler as an opaque "chart not found".
	if _, err := semver.NewVersion(app.Spec.Version); err != nil {
		errs = append(errs, field.Invalid(spec.Child("version"), app.Spec.Version,
			fmt.Sprintf("must be a valid semantic version: %v", err)))
	}

	// target namespace must be a valid DNS-1123 label (the rule the API server applies to a
	// Namespace name), caught here so we never try to create an invalid namespace.
	for _, msg := range validation.IsDNS1123Label(app.Spec.Namespace) {
		errs = append(errs, field.Invalid(spec.Child("namespace"), app.Spec.Namespace, msg))
	}

	// if a values Secret is referenced, its name must be non-empty.
	if app.Spec.ValuesSecretRef != nil && app.Spec.ValuesSecretRef.Name == "" {
		errs = append(errs, field.Required(spec.Child("valuesSecretRef", "name"),
			"must be set when valuesSecretRef is provided"))
	}

	if len(errs) == 0 {
		return nil
	}
	return apierrors.NewInvalid(
		schema.GroupKind{Group: appsv1alpha1.GroupVersion.Group, Kind: "Application"},
		app.Name, errs)
}
