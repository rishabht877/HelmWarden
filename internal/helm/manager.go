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

// Package helm wraps the Helm v4 Go SDK so the controller can drive releases
// (install / upgrade / uninstall / rollback) against the cluster the operator
// runs in, without shelling out to the helm binary.
package helm

import (
	"fmt"
	"os"
	"path/filepath"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/cli"
	"k8s.io/client-go/rest"
)

// storageDriver keeps Helm release state as Secrets in the target namespace (the SDK default).
const storageDriver = "secret"

// Manager builds per-namespace Helm action configurations from the operator's in-cluster
// REST config. One Manager is shared across reconciles; a fresh action.Configuration is
// created per target namespace because release storage is namespace-scoped.
type Manager struct {
	restConfig *rest.Config
	settings   *cli.EnvSettings
}

// NewManager returns a Manager bound to the given in-cluster REST config. Helm's
// cache/config/data homes are redirected to a writable directory first, so chart pulls
// work even on a read-only (distroless) root filesystem.
func NewManager(cfg *rest.Config) (*Manager, error) {
	if err := ensureWritableHelmDirs(); err != nil {
		return nil, err
	}
	return &Manager{
		restConfig: cfg,
		settings:   cli.New(), // reads HELM_* env, which ensureWritableHelmDirs has set
	}, nil
}

// newActionConfig builds an action.Configuration scoped to a target namespace.
func (m *Manager) newActionConfig(namespace string) (*action.Configuration, error) {
	getter := newRESTClientGetter(m.restConfig, namespace)
	cfg := new(action.Configuration)
	// Helm v4's Init takes (getter, namespace, driver) — the v3 debug-log arg is gone.
	if err := cfg.Init(getter, namespace, storageDriver); err != nil {
		return nil, fmt.Errorf("init helm action config for namespace %q: %w", namespace, err)
	}
	return cfg, nil
}

// ensureWritableHelmDirs points HELM_CACHE_HOME/CONFIG_HOME/DATA_HOME at a writable base
// (default /tmp/helm) unless already set. cli.New() reads these at construction, so this
// must run before it. Without it, LocateChart fails writing the repo index on a read-only FS.
func ensureWritableHelmDirs() error {
	base := os.Getenv("HELM_HOME_BASE")
	if base == "" {
		base = filepath.Join(os.TempDir(), "helm")
	}
	for env, sub := range map[string]string{
		"HELM_CACHE_HOME":  "cache",
		"HELM_CONFIG_HOME": "config",
		"HELM_DATA_HOME":   "data",
	} {
		if os.Getenv(env) != "" {
			continue
		}
		dir := filepath.Join(base, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create helm dir %q: %w", dir, err)
		}
		if err := os.Setenv(env, dir); err != nil {
			return fmt.Errorf("set %s: %w", env, err)
		}
	}
	return nil
}
