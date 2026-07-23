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

package helm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/kube"
	release "helm.sh/helm/v4/pkg/release"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"
)

const (
	defaultMaxHistory = 10
	// defaultTimeout bounds how long Helm waits on hooks. Workload readiness is NOT waited on
	// here (see waitStrategy) — the controller tracks that asynchronously via requeue.
	defaultTimeout = 5 * time.Minute
	// waitStrategy is HookOnly so install/upgrade apply the manifests and return without
	// blocking on pod readiness. Helm v4 requires a strategy to be set explicitly (unlike v3).
	waitStrategy = kube.HookOnlyStrategy
)

// ReleaseSpec is the subset of an Application spec the Helm layer needs to drive a release.
type ReleaseSpec struct {
	ReleaseName string
	ChartName   string
	RepoURL     string
	Version     string
	Namespace   string
	Values      map[string]any
}

// ReleaseResult summarizes the state of a release after an action.
type ReleaseResult struct {
	Name     string
	Revision int
	Status   string
	Manifest string
}

// ParseValues turns a raw values.yaml document into the nested map Helm consumes.
func ParseValues(raw []byte) (map[string]any, error) {
	vals, err := common.ReadValues(raw)
	if err != nil {
		return nil, fmt.Errorf("parse values: %w", err)
	}
	return vals, nil
}

// InstallOrUpgrade installs the release if it has no history, otherwise upgrades it —
// the programmatic equivalent of `helm upgrade --install`.
func (m *Manager) InstallOrUpgrade(ctx context.Context, spec ReleaseSpec) (*ReleaseResult, error) {
	cfg, err := m.newActionConfig(spec.Namespace)
	if err != nil {
		return nil, err
	}

	ch, err := m.loadChart(spec)
	if err != nil {
		return nil, err
	}

	vals := spec.Values
	if vals == nil {
		vals = map[string]any{}
	}

	exists, err := releaseExists(cfg, spec.ReleaseName)
	if err != nil {
		return nil, err
	}

	var raw release.Releaser
	if exists {
		upg := action.NewUpgrade(cfg)
		upg.Namespace = spec.Namespace
		upg.MaxHistory = defaultMaxHistory
		upg.WaitStrategy = waitStrategy
		upg.Timeout = defaultTimeout
		if raw, err = upg.RunWithContext(ctx, spec.ReleaseName, ch, vals); err != nil {
			return nil, fmt.Errorf("helm upgrade %q: %w", spec.ReleaseName, err)
		}
	} else {
		inst := action.NewInstall(cfg)
		inst.ReleaseName = spec.ReleaseName
		inst.Namespace = spec.Namespace
		inst.CreateNamespace = false // the operator owns target namespaces itself
		inst.WaitStrategy = waitStrategy
		inst.Timeout = defaultTimeout
		if raw, err = inst.RunWithContext(ctx, ch, vals); err != nil {
			return nil, fmt.Errorf("helm install %q: %w", spec.ReleaseName, err)
		}
	}

	return toResult(raw)
}

// loadChart resolves the chart from its repo (downloading into the Helm cache if needed) and loads it.
func (m *Manager) loadChart(spec ReleaseSpec) (chart.Charter, error) {
	cpo := action.ChartPathOptions{RepoURL: spec.RepoURL, Version: spec.Version}
	path, err := cpo.LocateChart(spec.ChartName, m.settings)
	if err != nil {
		return nil, fmt.Errorf("locate chart %q (%s@%s): %w", spec.ChartName, spec.RepoURL, spec.Version, err)
	}
	ch, err := loader.Load(path)
	if err != nil {
		return nil, fmt.Errorf("load chart from %q: %w", path, err)
	}
	return ch, nil
}

// releaseExists reports whether a release with the given name already has stored history.
func releaseExists(cfg *action.Configuration, name string) (bool, error) {
	_, err := action.NewHistory(cfg).Run(name)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, driver.ErrReleaseNotFound):
		return false, nil
	default:
		return false, fmt.Errorf("helm history %q: %w", name, err)
	}
}

// toResult extracts the fields we track from the concrete release Helm returns behind its interface.
func toResult(raw release.Releaser) (*ReleaseResult, error) {
	rel, ok := raw.(*releasev1.Release)
	if !ok {
		return nil, fmt.Errorf("unexpected release type %T returned by helm", raw)
	}
	res := &ReleaseResult{
		Name:     rel.Name,
		Revision: rel.Version,
		Manifest: rel.Manifest,
	}
	if rel.Info != nil {
		res.Status = rel.Info.Status.String()
	}
	return res, nil
}
