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
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	memcache "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// restClientGetter is a genericclioptions.RESTClientGetter built from an in-cluster
// *rest.Config rather than an on-disk kubeconfig. Using controller-runtime's config here
// (not genericclioptions.NewConfigFlags, which reads ~/.kube/config) is what lets Helm
// resolve GVKs when the operator runs inside the cluster.
//
// The discovery client is memory-cached and the RESTMapper is deferred, so types belonging
// to CRDs a chart installs can be discovered after a Reset without rebuilding the getter.
type restClientGetter struct {
	restConfig *rest.Config
	namespace  string

	mu     sync.Mutex
	mapper meta.RESTMapper
}

// newRESTClientGetter returns a getter bound to a copy of cfg, scoped to namespace.
func newRESTClientGetter(cfg *rest.Config, namespace string) *restClientGetter {
	return &restClientGetter{
		restConfig: rest.CopyConfig(cfg),
		namespace:  namespace,
	}
}

// ToRESTConfig returns the in-cluster REST config Helm uses to build its clients.
func (g *restClientGetter) ToRESTConfig() (*rest.Config, error) {
	return g.restConfig, nil
}

// ToDiscoveryClient returns a memory-cached discovery client. Caching matters because Helm
// hits discovery repeatedly per action to resolve resource kinds.
func (g *restClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(g.restConfig)
	if err != nil {
		return nil, err
	}
	return memcache.NewMemCacheClient(dc), nil
}

// ToRESTMapper returns a deferred, discovery-backed RESTMapper, cached for the getter's life.
func (g *restClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.mapper != nil {
		return g.mapper, nil
	}
	dc, err := g.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}
	g.mapper = restmapper.NewDeferredDiscoveryRESTMapper(dc)
	return g.mapper, nil
}

// ToRawKubeConfigLoader returns an in-memory client config carrying only the target
// namespace. Helm calls .Namespace() on this loader but builds its actual clients from
// ToRESTConfig, so nothing on disk is ever read.
func (g *restClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	overrides := &clientcmd.ConfigOverrides{
		Context: clientcmdapi.Context{Namespace: g.namespace},
	}
	return clientcmd.NewDefaultClientConfig(*clientcmdapi.NewConfig(), overrides)
}

// compile-time assertion that we satisfy the interface Helm's action.Configuration.Init wants.
var _ genericclioptions.RESTClientGetter = (*restClientGetter)(nil)
