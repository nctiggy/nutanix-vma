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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

const (
	providerFinalizer = "vma.nutanix.io/provider-protection"

	conditionTypeConnected      = "Connected"
	conditionTypeInventoryReady = "InventoryReady"
)

// NutanixClientFactory creates Nutanix API clients. Allows injection for testing.
type NutanixClientFactory func(config nutanix.ClientConfig) (nutanix.NutanixClient, error)

// ProviderReconciler reconciles NutanixProvider objects.
type ProviderReconciler struct {
	client.Client
	ClientFactory NutanixClientFactory
}

// SetupProviderController registers the Provider reconciler with the manager.
func SetupProviderController(mgr ctrl.Manager) error {
	return (&ProviderReconciler{
		Client:        mgr.GetClient(),
		ClientFactory: nutanix.NewClient,
	}).SetupWithManager(mgr)
}

// +kubebuilder:rbac:groups=vma.nutanix.io,resources=nutanixproviders,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=nutanixproviders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=nutanixproviders/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile handles NutanixProvider reconciliation.
func (r *ProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the provider
	provider := &vmav1alpha1.NutanixProvider{}
	if err := r.Get(ctx, req.NamespacedName, provider); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !provider.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(provider, providerFinalizer) {
			// Check if any MigrationPlans reference this provider
			inUse, err := r.isProviderInUse(ctx, provider)
			if err != nil {
				return ctrl.Result{}, err
			}
			if inUse {
				logger.Info("Provider still referenced by MigrationPlans, cannot remove finalizer")
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}

			controllerutil.RemoveFinalizer(provider, providerFinalizer)
			if err := r.Update(ctx, provider); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure finalizer -- return immediately after adding it to avoid
	// resource version conflicts between metadata and status updates.
	if !controllerutil.ContainsFinalizer(provider, providerFinalizer) {
		controllerutil.AddFinalizer(provider, providerFinalizer)
		if err := r.Update(ctx, provider); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Set phase to Connecting
	provider.Status.Phase = vmav1alpha1.ProviderPhaseConnecting
	if err := r.Status().Update(ctx, provider); err != nil {
		return ctrl.Result{}, err
	}

	// Read credentials from Secret
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{
		Name:      provider.Spec.SecretRef.Name,
		Namespace: provider.Namespace,
	}
	if err := r.Get(ctx, secretKey, secret); err != nil {
		logger.Error(err, "Failed to read credentials secret")
		return r.setErrorCondition(ctx, provider, "SecretNotFound",
			fmt.Sprintf("Secret %q not found: %v", provider.Spec.SecretRef.Name, err))
	}

	username := string(secret.Data["username"])
	password := string(secret.Data["password"])
	if username == "" || password == "" {
		return r.setErrorCondition(ctx, provider, "InvalidCredentials",
			"Secret must contain non-empty 'username' and 'password' keys")
	}

	// Create Nutanix client
	nxClient, err := r.ClientFactory(nutanix.ClientConfig{
		Host:               provider.Spec.URL,
		Username:           username,
		Password:           password,
		InsecureSkipVerify: provider.Spec.InsecureSkipVerify,
	})
	if err != nil {
		logger.Error(err, "Failed to create Nutanix client")
		return r.setErrorCondition(ctx, provider, "ClientError",
			fmt.Sprintf("Failed to create client: %v", err))
	}

	// Fetch inventory
	vms, err := nxClient.ListVMs(ctx)
	if err != nil {
		logger.Error(err, "Failed to list VMs")
		return r.setErrorCondition(ctx, provider, "ConnectionFailed",
			fmt.Sprintf("Failed to list VMs: %v", err))
	}

	// Set Connected condition
	meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
		Type:               conditionTypeConnected,
		Status:             metav1.ConditionTrue,
		Reason:             "Connected",
		Message:            fmt.Sprintf("Connected to %s", provider.Spec.URL),
		ObservedGeneration: provider.Generation,
	})

	// Fetch subnets (best-effort for inventory)
	subnets, err := nxClient.ListSubnets(ctx)
	if err != nil {
		logger.Error(err, "Failed to list subnets (continuing with partial inventory)")
	}

	// Fetch clusters
	clusters, err := nxClient.ListClusters(ctx)
	if err != nil {
		logger.Error(err, "Failed to list clusters (continuing with partial inventory)")
	}

	// Fetch storage containers per PE cluster
	var storageContainerCount int
	for _, cluster := range clusters {
		if cluster.Network == nil || cluster.Network.ExternalAddress == "" {
			continue
		}
		peURL := fmt.Sprintf("https://%s:9440", cluster.Network.ExternalAddress)
		containers, scErr := nxClient.ListStorageContainers(ctx, peURL)
		if scErr != nil {
			logger.Error(scErr, "Failed to list storage containers",
				"cluster", cluster.Name, "peURL", peURL)
			continue
		}
		storageContainerCount += len(containers)
	}

	// Update status
	provider.Status.Phase = vmav1alpha1.ProviderPhaseConnected
	provider.Status.VMCount = len(vms)

	meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
		Type:   conditionTypeInventoryReady,
		Status: metav1.ConditionTrue,
		Reason: "InventoryComplete",
		Message: fmt.Sprintf(
			"Discovered %d VMs, %d subnets, %d clusters, %d storage containers",
			len(vms), len(subnets), len(clusters), storageContainerCount),
		ObservedGeneration: provider.Generation,
	})

	if err := r.Status().Update(ctx, provider); err != nil {
		return ctrl.Result{}, err
	}

	// Parse refresh interval and requeue
	refreshInterval, err := time.ParseDuration(provider.Spec.RefreshInterval)
	if err != nil {
		refreshInterval = 5 * time.Minute
	}

	logger.Info("Provider reconciliation complete",
		"vmCount", len(vms),
		"requeueAfter", refreshInterval)

	return ctrl.Result{RequeueAfter: refreshInterval}, nil
}

// setErrorCondition updates the provider status to Error phase with conditions.
func (r *ProviderReconciler) setErrorCondition(
	ctx context.Context,
	provider *vmav1alpha1.NutanixProvider,
	reason, message string,
) (ctrl.Result, error) {
	provider.Status.Phase = vmav1alpha1.ProviderPhaseError

	meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
		Type:               conditionTypeConnected,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: provider.Generation,
	})

	if err := r.Status().Update(ctx, provider); err != nil {
		return ctrl.Result{}, err
	}

	// Requeue after 1 minute to retry on transient errors
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

// isProviderInUse checks if any MigrationPlan references this provider.
func (r *ProviderReconciler) isProviderInUse(
	ctx context.Context,
	provider *vmav1alpha1.NutanixProvider,
) (bool, error) {
	plans := &vmav1alpha1.MigrationPlanList{}
	if err := r.List(ctx, plans, client.InNamespace(provider.Namespace)); err != nil {
		return false, err
	}

	for i := range plans.Items {
		if plans.Items[i].Spec.ProviderRef.Name == provider.Name {
			return true, nil
		}
	}
	return false, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vmav1alpha1.NutanixProvider{}).
		Named("provider").
		Complete(r)
}
