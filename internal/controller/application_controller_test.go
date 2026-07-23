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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appsv1alpha1 "github.com/rishabht877/HelmWarden/api/v1alpha1"
	"github.com/rishabht877/HelmWarden/internal/helm"
)

// These tests run against envtest, which has an API server but no kubelet — so a real Helm install
// can't converge here. We therefore verify the API-level behavior (CRD defaulting) and the
// reconciler's first-pass finalizer registration, which returns before any Helm call. The Helm
// action path itself is unit-tested in the internal/helm package.
var _ = Describe("Application Controller", func() {
	Context("When reconciling a newly created Application", func() {
		const (
			resourceName      = "test-resource"
			resourceNamespace = "default"
		)

		ctx := context.Background()
		key := types.NamespacedName{Name: resourceName, Namespace: resourceNamespace}

		newApplication := func() *appsv1alpha1.Application {
			return &appsv1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: resourceNamespace},
				Spec: appsv1alpha1.ApplicationSpec{
					ChartName: "podinfo",
					RepoURL:   "https://stefanprodan.github.io/podinfo",
					Version:   "6.7.1",
					Namespace: "demo",
				},
			}
		}

		AfterEach(func() {
			resource := &appsv1alpha1.Application{}
			if err := k8sClient.Get(ctx, key, resource); err == nil {
				// No controller runs in envtest, so clear the finalizer ourselves to allow deletion.
				resource.Finalizers = nil
				Expect(k8sClient.Update(ctx, resource)).To(Succeed())
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}
		})

		It("applies CRD defaults on create", func() {
			Expect(k8sClient.Create(ctx, newApplication())).To(Succeed())

			created := &appsv1alpha1.Application{}
			Expect(k8sClient.Get(ctx, key, created)).To(Succeed())
			Expect(created.Spec.ProgressDeadlineSeconds).To(Equal(int32(300)))
		})

		It("registers the finalizer on the first reconcile", func() {
			Expect(k8sClient.Create(ctx, newApplication())).To(Succeed())

			helmManager, err := helm.NewManager(cfg)
			Expect(err).NotTo(HaveOccurred())
			reconciler := &ApplicationReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Helm:   helmManager,
			}

			// The first pass adds the finalizer and returns before touching Helm.
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			got := &appsv1alpha1.Application{}
			Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
			Expect(got.Finalizers).To(ContainElement(finalizerName))
		})
	})
})
