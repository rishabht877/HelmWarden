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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appsv1alpha1 "github.com/rishabht877/HelmWarden/api/v1alpha1"
)

var _ = Describe("Application Webhook", func() {
	var validator ApplicationCustomValidator
	ctx := context.Background()

	validApp := func() *appsv1alpha1.Application {
		return &appsv1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{Name: "sample", Namespace: "default"},
			Spec: appsv1alpha1.ApplicationSpec{
				ChartName: "podinfo",
				RepoURL:   "https://stefanprodan.github.io/podinfo",
				Version:   "6.7.1",
				Namespace: "demo",
			},
		}
	}

	BeforeEach(func() {
		validator = ApplicationCustomValidator{}
	})

	Context("ValidateCreate", func() {
		It("admits a valid Application", func() {
			_, err := validator.ValidateCreate(ctx, validApp())
			Expect(err).NotTo(HaveOccurred())
		})

		It("rejects a non-semver version", func() {
			app := validApp()
			app.Spec.Version = "not-a-version"
			_, err := validator.ValidateCreate(ctx, app)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("version"))
		})

		It("rejects an invalid target namespace", func() {
			app := validApp()
			app.Spec.Namespace = "Invalid_NS"
			_, err := validator.ValidateCreate(ctx, app)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("namespace"))
		})

		It("rejects a valuesSecretRef with an empty name", func() {
			app := validApp()
			app.Spec.ValuesSecretRef = &appsv1alpha1.ValuesSecretRef{Name: ""}
			_, err := validator.ValidateCreate(ctx, app)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("valuesSecretRef"))
		})

		It("aggregates multiple problems into one error", func() {
			app := validApp()
			app.Spec.Version = "nope"
			app.Spec.Namespace = "BAD"
			_, err := validator.ValidateCreate(ctx, app)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(And(ContainSubstring("version"), ContainSubstring("namespace")))
		})
	})

	Context("ValidateUpdate", func() {
		It("rejects an update that introduces a bad version", func() {
			_, err := validator.ValidateUpdate(ctx, validApp(), func() *appsv1alpha1.Application {
				app := validApp()
				app.Spec.Version = "bad"
				return app
			}())
			Expect(err).To(HaveOccurred())
		})
	})

	Context("ValidateDelete", func() {
		It("always admits deletion", func() {
			_, err := validator.ValidateDelete(ctx, validApp())
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
