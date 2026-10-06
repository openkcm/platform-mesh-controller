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

package main

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"context"
	"flag"
	"os"
	"strings"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/kcp-dev/multicluster-provider/apiexport"
	kcpapisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	kcpapisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	kcptenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"

	operationsv1alpha1 "github.com/openkcm/openkcm-controller/api/operations/v1alpha1"
	operationscontroller "github.com/openkcm/openkcm-controller/internal/controller/operations"
	"github.com/openkcm/openkcm-controller/internal/kryptongrpc"
	"github.com/openkcm/openkcm-controller/internal/mockapi"
	"github.com/openkcm/openkcm-controller/internal/openkcmapi"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kcpapisv1alpha1.AddToScheme(scheme))
	utilruntime.Must(kcpapisv1alpha2.AddToScheme(scheme))
	utilruntime.Must(kcpcorev1alpha1.AddToScheme(scheme))
	utilruntime.Must(kcptenancyv1alpha1.AddToScheme(scheme))
	utilruntime.Must(operationsv1alpha1.AddToScheme(scheme))
}

func main() {
	var kcpKubeconfig string
	var operationsEndpointSlice string
	var mockAPIAddr string
	var openkcmAPIURL string
	var kryptonAddr string
	var metricsAddr string
	var metricsSecure bool
	var probeAddr string
	var enableLeaderElection bool
	var localHealthOnly bool

	// Defaults applied by AccountBootstrapReconciler when minting a Tenant.
	var defaultRegion string
	var defaultOIDCIssuer string
	var defaultOIDCJWKSURI string
	var defaultOIDCAudiencesCSV string
	var tenantNamespace string

	flag.StringVar(&kcpKubeconfig, "kcp-kubeconfig", "", "Path to KCP kubeconfig")
	flag.StringVar(&operationsEndpointSlice,
		"operations-apiexport-endpointslice",
		"operations.openkcm.io",
		"Operations APIExportEndpointSlice name",
	)
	flag.StringVar(&mockAPIAddr, "mock-api-addr", ":9090", "Mock API server listen address")
	flag.StringVar(&openkcmAPIURL, "openkcm-api-url", "http://localhost:9090", "OpenKCM API base URL")
	flag.StringVar(&kryptonAddr,
		"krypton-grpc-addr",
		"",
		"Krypton admin gRPC address (host:port). When set, Tenant, DomainKey, ServiceKey and DataEncryptionKey "+
			"reconciliation talk to Krypton instead of the OpenKCM HTTP API. The "+
			"connection is currently plaintext: transport security for the showroom "+
			"gateway is unresolved.",
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Metrics bind address")
	flag.BoolVar(&metricsSecure, "metrics-secure", false, "Serve metrics over HTTPS with Kubernetes authn/authz")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health probe bind address")
	flag.BoolVar(&enableLeaderElection,
		"leader-elect",
		false,
		"Enable leader election for controller manager. Enabling this ensures there is only one active controller manager.",
	)
	flag.BoolVar(&localHealthOnly,
		"local-health-only",
		false,
		"Start only local health and metrics endpoints. "+
			"Intended for generated Kind E2E smoke tests that do not provide KCP APIs.",
	)
	flag.StringVar(&defaultRegion,
		"default-tenant-region",
		"eu-central",
		"Region used when auto-creating Tenant CRs for newly engaged account workspaces",
	)
	flag.StringVar(&defaultOIDCIssuer,
		"default-tenant-oidc-issuer",
		"",
		"OIDC issuer URL used when auto-creating Tenant CRs (optional)",
	)
	flag.StringVar(&defaultOIDCJWKSURI,
		"default-tenant-oidc-jwks-uri",
		"",
		"OIDC JWKS URI used when auto-creating Tenant CRs (optional)",
	)
	flag.StringVar(&defaultOIDCAudiencesCSV,
		"default-tenant-oidc-audiences",
		"",
		"Comma-separated OIDC audiences used when auto-creating Tenant CRs (optional)",
	)
	flag.StringVar(&tenantNamespace, "tenant-namespace", "default", "Namespace used when auto-creating Tenant CRs")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	metricsOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: metricsSecure,
	}

	if localHealthOnly {
		localMgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), manager.Options{
			Scheme:                 scheme,
			Metrics:                metricsOptions,
			HealthProbeBindAddress: probeAddr,
			LeaderElection:         enableLeaderElection,
			LeaderElectionID:       "openkcm.local-health.operations.openkcm.io",
		})
		if err != nil {
			setupLog.Error(err, "Failed to create local health-only manager")
			os.Exit(1)
		}
		if err := localMgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
			setupLog.Error(err, "Failed to set up health check")
			os.Exit(1)
		}
		if err := localMgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
			setupLog.Error(err, "Failed to set up ready check")
			os.Exit(1)
		}

		setupLog.Info("Starting local health-only manager")
		if err := localMgr.Start(ctrl.SetupSignalHandler()); err != nil {
			setupLog.Error(err, "Local health-only manager exited with error")
			os.Exit(1)
		}
		return
	}

	// Start mock API server in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		setupLog.Info("Starting mock OpenKCM API server", "addr", mockAPIAddr)
		if err := mockapi.Start(ctx, mockAPIAddr); err != nil {
			setupLog.Error(err, "Mock API server failed")
		}
	}()

	apiClient := openkcmapi.NewClient(openkcmAPIURL)

	// Tenant, DomainKey, ServiceKey and DataEncryptionKey are the slices moved onto Krypton's real gRPC API.
	// Every other reconciler still speaks the mock's HTTP surface, so both
	// clients coexist until each slice is migrated in turn.
	var tenantBackend operationscontroller.TenantBackend = apiClient
	var keyBackend operationscontroller.KeyBackend = apiClient
	var dataEncryptionKeyBackend operationscontroller.DataEncryptionKeyBackend = apiClient
	if kryptonAddr != "" {
		conn, err := grpc.NewClient(kryptonAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			setupLog.Error(err, "Failed to connect to Krypton", "addr", kryptonAddr)
			os.Exit(1)
		}
		defer func() { _ = conn.Close() }()
		tenantBackend = kryptongrpc.NewTenantClient(conn)
		kryptonBackend := kryptongrpc.NewBackend(conn, kryptongrpc.BackendOptions{})
		keyBackend = kryptonBackend
		dataEncryptionKeyBackend = kryptonBackend
		setupLog.Info("Tenant, DomainKey, ServiceKey and DataEncryptionKey reconciliation bound to Krypton",
			"addr", kryptonAddr)
	}

	kcpCfg, err := clientcmd.BuildConfigFromFlags("", kcpKubeconfig)
	if err != nil {
		setupLog.Error(err, "Failed to load KCP kubeconfig")
		os.Exit(1)
	}

	// Single APIExport provider — operations.openkcm.io carries every
	// kind after v0.7.0 (Tenant, DomainKey, ServiceKey, L1 root keys,
	// DataEncryptionKey). The retired identity APIExport is no longer registered.
	opsProvider, err := apiexport.New(kcpCfg, operationsEndpointSlice, apiexport.Options{
		Scheme: scheme,
	})
	if err != nil {
		setupLog.Error(err, "Failed to create operations APIExport provider")
		os.Exit(1)
	}

	opsMgr, err := mcmanager.New(kcpCfg, opsProvider, manager.Options{
		Scheme:                 scheme,
		Metrics:                metricsOptions,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "openkcm.operations.openkcm.io",
	})
	if err != nil {
		setupLog.Error(err, "Failed to create operations manager")
		os.Exit(1)
	}

	// Register controllers — all in the operations package.
	var defaultOIDCAudiences []string
	if defaultOIDCAudiencesCSV != "" {
		for s := range strings.SplitSeq(defaultOIDCAudiencesCSV, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				defaultOIDCAudiences = append(defaultOIDCAudiences, s)
			}
		}
	}

	if err := (&operationscontroller.AccountBootstrapReconciler{
		DefaultRegion:        defaultRegion,
		DefaultOIDCIssuer:    defaultOIDCIssuer,
		DefaultOIDCJWKSURI:   defaultOIDCJWKSURI,
		DefaultOIDCAudiences: defaultOIDCAudiences,
		TenantNamespace:      tenantNamespace,
	}).SetupWithManager(opsMgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "AccountBootstrap")
		os.Exit(1)
	}

	if err := (&operationscontroller.NamespaceBootstrapReconciler{
		AccountNamespace: tenantNamespace,
	}).SetupWithManager(opsMgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "NamespaceBootstrap")
		os.Exit(1)
	}

	if err := (&operationscontroller.TenantReconciler{APIClient: tenantBackend}).SetupWithManager(opsMgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "Tenant")
		os.Exit(1)
	}

	if err := (&operationscontroller.DomainKeyReconciler{
		APIClient:        keyBackend,
		AccountNamespace: tenantNamespace,
	}).SetupWithManager(opsMgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "DomainKey")
		os.Exit(1)
	}

	if err := (&operationscontroller.ServiceKeyReconciler{
		APIClient:        keyBackend,
		AccountNamespace: tenantNamespace,
	}).SetupWithManager(opsMgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "ServiceKey")
		os.Exit(1)
	}

	if err := (&operationscontroller.AWSRootKeyReconciler{
		APIClient:        apiClient,
		AccountNamespace: tenantNamespace,
	}).SetupWithManager(opsMgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "AWSRootKey")
		os.Exit(1)
	}

	if err := (&operationscontroller.AzureRootKeyReconciler{
		APIClient:        apiClient,
		AccountNamespace: tenantNamespace,
	}).SetupWithManager(opsMgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "AzureRootKey")
		os.Exit(1)
	}

	if err := (&operationscontroller.OpenBaoRootKeyReconciler{
		APIClient:        apiClient,
		AccountNamespace: tenantNamespace,
	}).SetupWithManager(opsMgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "OpenBaoRootKey")
		os.Exit(1)
	}

	if err := (&operationscontroller.DataEncryptionKeyReconciler{
		APIClient:        dataEncryptionKeyBackend,
		AccountNamespace: tenantNamespace,
	}).SetupWithManager(opsMgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "DataEncryptionKey")
		os.Exit(1)
	}

	if err := opsMgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := opsMgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	sigCtx := ctrl.SetupSignalHandler()
	setupLog.Info("Starting operations manager")
	if err := opsMgr.Start(sigCtx); err != nil {
		setupLog.Error(err, "Manager exited with error")
		os.Exit(1)
	}
}
