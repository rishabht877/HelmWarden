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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ApplicationPhase is a high-level summary of where an Application is in its lifecycle.
// +kubebuilder:validation:Enum=Pending;Deploying;Deployed;Degraded;RollingBack;Failed
type ApplicationPhase string

const (
	// PhasePending means the Application has been accepted but no Helm action has run yet.
	PhasePending ApplicationPhase = "Pending"
	// PhaseDeploying means a Helm install/upgrade has run and workloads are converging.
	PhaseDeploying ApplicationPhase = "Deploying"
	// PhaseDeployed means the release is installed and all workloads are healthy.
	PhaseDeployed ApplicationPhase = "Deployed"
	// PhaseDegraded means the release is installed but workloads are unhealthy (post-rollback or awaiting fix).
	PhaseDegraded ApplicationPhase = "Degraded"
	// PhaseRollingBack means an unhealthy rollout is being rolled back to the previous good revision.
	PhaseRollingBack ApplicationPhase = "RollingBack"
	// PhaseFailed means the release could not be reconciled and no rollback target exists.
	PhaseFailed ApplicationPhase = "Failed"
)

// Condition types set on an Application's status.
const (
	// ConditionReleased is True once the Helm install/upgrade has succeeded.
	ConditionReleased = "Released"
	// ConditionHealthy is True once all workloads in the release report Current.
	ConditionHealthy = "Healthy"
	// ConditionReady is the roll-up: True when the Application is fully deployed and healthy.
	ConditionReady = "Ready"
)

// ValuesSecretRef references a Secret holding Helm values overrides.
type ValuesSecretRef struct {
	// name is the Secret name, resolved in the Application's own namespace.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// key is the Secret data key holding a full values.yaml document.
	// +kubebuilder:default=values.yaml
	// +optional
	Key string `json:"key,omitempty"`
}

// ApplicationSpec defines the desired state of Application.
type ApplicationSpec struct {
	// chartName is the name of the Helm chart to deploy (e.g. "nginx").
	// +required
	// +kubebuilder:validation:MinLength=1
	ChartName string `json:"chartName"`

	// repoURL is the Helm chart repository URL hosting the chart.
	// +required
	// +kubebuilder:validation:MinLength=1
	RepoURL string `json:"repoURL"`

	// version is the chart version to deploy. Must be valid semver (enforced by the admission webhook).
	// +required
	// +kubebuilder:validation:MinLength=1
	Version string `json:"version"`

	// namespace is the target namespace for the Helm release. Created and managed by the
	// operator if absent.
	// +required
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// valuesSecretRef optionally references a Secret (in the Application's namespace) whose key holds
	// Helm value overrides as a values.yaml document.
	// +optional
	ValuesSecretRef *ValuesSecretRef `json:"valuesSecretRef,omitempty"`

	// progressDeadlineSeconds is how long to wait for the release to become healthy before treating the
	// rollout as failed and triggering a rollback. Defaults to 300.
	// +kubebuilder:default=300
	// +kubebuilder:validation:Minimum=10
	// +optional
	ProgressDeadlineSeconds int32 `json:"progressDeadlineSeconds,omitempty"`
}

// ApplicationStatus defines the observed state of Application.
type ApplicationStatus struct {
	// phase is a high-level summary of the Application lifecycle state.
	// +optional
	Phase ApplicationPhase `json:"phase,omitempty"`

	// conditions represent the current state of the Application resource.
	// Types: "Released", "Healthy", "Ready".
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// observedGeneration is the .metadata.generation the controller last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// helmReleaseName is the name of the managed Helm release.
	// +optional
	HelmReleaseName string `json:"helmReleaseName,omitempty"`

	// helmRevision mirrors the current Helm release revision (cross-checkable with `helm history`).
	// +optional
	HelmRevision int `json:"helmRevision,omitempty"`

	// lastAppliedValuesHash is a hash of the values last applied, used to short-circuit no-op upgrades.
	// +optional
	LastAppliedValuesHash string `json:"lastAppliedValuesHash,omitempty"`

	// lastFailedRevision records the last release revision that failed health checks, to prevent
	// rollback thrash.
	// +optional
	LastFailedRevision int `json:"lastFailedRevision,omitempty"`

	// lastDeployStartTime marks when the most recent install/upgrade began, used for the progress deadline.
	// +optional
	LastDeployStartTime *metav1.Time `json:"lastDeployStartTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Chart",type=string,JSONPath=`.spec.chartName`
// +kubebuilder:printcolumn:name="Revision",type=integer,JSONPath=`.status.helmRevision`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Application is the Schema for the applications API.
type Application struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Application
	// +required
	Spec ApplicationSpec `json:"spec"`

	// status defines the observed state of Application
	// +optional
	Status ApplicationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ApplicationList contains a list of Application.
type ApplicationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Application `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Application{}, &ApplicationList{})
		return nil
	})
}
