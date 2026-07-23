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
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/cli-utils/pkg/kstatus/status"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
