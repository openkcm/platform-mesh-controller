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

package operations

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	"sigs.k8s.io/multicluster-runtime/providers/single"
)

const (
	testClusterName = "test-workspace"
	testAccountName = "acme-prod"
	testWorkspace   = "root:orgs:acme:" + testAccountName
)

// newTestManager wires a multicluster manager over the envtest API server. The
// client is deliberately uncached: nothing starts the manager in these tests,
// so a cache-backed reader would never sync.
func newTestManager() mcmanager.Manager {
	GinkgoHelper()

	cl, err := cluster.New(cfg, func(o *cluster.Options) {
		o.Scheme = scheme.Scheme
		o.NewClient = func(c *rest.Config, opts client.Options) (client.Client, error) {
			opts.Cache = nil
			return client.New(c, opts)
		}
	})
	Expect(err).NotTo(HaveOccurred())

	mgr, err := mcmanager.New(cfg, single.New(testClusterName, cl), manager.Options{
		Scheme:         scheme.Scheme,
		Metrics:        metricsserver.Options{BindAddress: "0"},
		LeaderElection: false,
	})
	Expect(err).NotTo(HaveOccurred())
	return mgr
}

// ensureLogicalCluster creates the object path.go derives the account name
// from. Named "cluster" because that is the only name kcp ever uses.
func ensureLogicalCluster(path string) {
	GinkgoHelper()

	lc := &kcpcorev1alpha1.LogicalCluster{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: "cluster"}, lc)
	if err == nil {
		lc.Annotations = map[string]string{pathAnnotation: path}
		Expect(k8sClient.Update(ctx, lc)).To(Succeed())
		return
	}

	lc = &kcpcorev1alpha1.LogicalCluster{}
	lc.Name = "cluster"
	lc.Annotations = map[string]string{pathAnnotation: path}
	Expect(k8sClient.Create(ctx, lc)).To(Succeed())
}

// requestFor builds a reconcile request for any object in the test workspace.
func requestFor(obj client.Object) mcreconcile.Request {
	return mcreconcile.Request{
		ClusterName: testClusterName,
		Name:        obj.GetName(),
		Namespace:   obj.GetNamespace(),
	}
}
