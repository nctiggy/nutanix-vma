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
	"sigs.k8s.io/controller-runtime/pkg/log"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
	"github.com/nctiggy/nutanix-vma/internal/validation"
)

const conditionTypeValidated = "Validated"

// PlanReconciler reconciles MigrationPlan objects.
type PlanReconciler struct {
	client.Client
	ClientFactory NutanixClientFactory
}

// SetupPlanController registers the Plan reconciler with the manager.
func SetupPlanController(mgr ctrl.Manager) error {
	return (&PlanReconciler{
		Client:        mgr.GetClient(),
		ClientFactory: nutanix.NewClient,
	}).SetupWithManager(mgr)
}

// +kubebuilder:rbac:groups=vma.nutanix.io,resources=migrationplans,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=migrationplans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=nutanixproviders,verbs=get
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=networkmaps,verbs=get
// +kubebuilder:rbac:groups=vma.nutanix.io,resources=storagemaps,verbs=get
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get

// Reconcile handles MigrationPlan reconciliation.
func (r *PlanReconciler) Reconcile(
	ctx context.Context, req ctrl.Request,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the plan
	plan := &vmav1alpha1.MigrationPlan{}
	if err := r.Get(ctx, req.NamespacedName, plan); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Skip re-validation if already validated for this generation
	for _, c := range plan.Status.Conditions {
		if c.Type == conditionTypeValidated &&
			c.ObservedGeneration == plan.Generation {
			return ctrl.Result{}, nil
		}
	}

	// Set phase to Validating
	plan.Status.Phase = vmav1alpha1.PlanPhaseValidating
	if err := r.Status().Update(ctx, plan); err != nil {
		return ctrl.Result{}, err
	}

	// Resolve Provider
	provider := &vmav1alpha1.NutanixProvider{}
	if err := r.Get(ctx, types.NamespacedName{
		Name: plan.Spec.ProviderRef.Name, Namespace: plan.Namespace,
	}, provider); err != nil {
		logger.Error(err, "Failed to resolve Provider")
		return r.setPlanError(ctx, plan, "ProviderNotFound",
			fmt.Sprintf("Provider %q not found: %v",
				plan.Spec.ProviderRef.Name, err))
	}

	// Read credentials from Provider's Secret
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      provider.Spec.SecretRef.Name,
		Namespace: provider.Namespace,
	}, secret); err != nil {
		logger.Error(err, "Failed to read Provider credentials secret")
		return r.setPlanError(ctx, plan, "SecretNotFound",
			fmt.Sprintf("Provider secret %q not found: %v",
				provider.Spec.SecretRef.Name, err))
	}

	username := string(secret.Data["username"])
	password := string(secret.Data["password"])
	if username == "" || password == "" {
		return r.setPlanError(ctx, plan, "InvalidCredentials",
			"Provider secret must contain non-empty "+
				"'username' and 'password' keys")
	}

	// Create Nutanix client from Provider credentials
	nxClient, err := r.ClientFactory(nutanix.ClientConfig{
		Host:               provider.Spec.URL,
		Username:           username,
		Password:           password,
		InsecureSkipVerify: provider.Spec.InsecureSkipVerify,
	})
	if err != nil {
		logger.Error(err, "Failed to create Nutanix client")
		return r.setPlanError(ctx, plan, "ClientError",
			fmt.Sprintf("Failed to create Nutanix client: %v", err))
	}

	// Resolve NetworkMap
	networkMap := &vmav1alpha1.NetworkMap{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      plan.Spec.NetworkMapRef.Name,
		Namespace: plan.Namespace,
	}, networkMap); err != nil {
		logger.Error(err, "Failed to resolve NetworkMap")
		return r.setPlanError(ctx, plan, "NetworkMapNotFound",
			fmt.Sprintf("NetworkMap %q not found: %v",
				plan.Spec.NetworkMapRef.Name, err))
	}

	// Resolve StorageMap
	storageMap := &vmav1alpha1.StorageMap{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      plan.Spec.StorageMapRef.Name,
		Namespace: plan.Namespace,
	}, storageMap); err != nil {
		logger.Error(err, "Failed to resolve StorageMap")
		return r.setPlanError(ctx, plan, "StorageMapNotFound",
			fmt.Sprintf("StorageMap %q not found: %v",
				plan.Spec.StorageMapRef.Name, err))
	}

	// Validate each VM in the plan
	hasErrors := false
	vmStatuses := make(
		[]vmav1alpha1.VMValidationStatus, 0, len(plan.Spec.VMs),
	)

	for _, planVM := range plan.Spec.VMs {
		vm, vmErr := nxClient.GetVM(ctx, planVM.ID)
		if vmErr != nil {
			logger.Error(vmErr, "Failed to fetch VM", "vmID", planVM.ID)
			vmStatuses = append(vmStatuses,
				vmav1alpha1.VMValidationStatus{
					ID:   planVM.ID,
					Name: planVM.Name,
					Concerns: []vmav1alpha1.Concern{{
						Category: vmav1alpha1.ConcernCategoryError,
						Message: fmt.Sprintf(
							"Failed to fetch VM from Nutanix: %v",
							vmErr),
					}},
				})
			hasErrors = true
			continue
		}

		concerns := validation.Validate(
			ctx, vm, validation.ValidationOptions{
				NetworkMap:      networkMap,
				StorageMap:      storageMap,
				TargetNamespace: plan.Spec.TargetNamespace,
				Client:          r.Client,
			})

		name := planVM.Name
		if name == "" {
			name = vm.Name
		}

		vmStatuses = append(vmStatuses,
			vmav1alpha1.VMValidationStatus{
				ID:       planVM.ID,
				Name:     name,
				Concerns: concerns,
			})
		if validation.HasErrors(concerns) {
			hasErrors = true
		}
	}

	// Update plan status
	plan.Status.VMs = vmStatuses
	if hasErrors {
		plan.Status.Phase = vmav1alpha1.PlanPhaseError
		meta.SetStatusCondition(&plan.Status.Conditions,
			metav1.Condition{
				Type:               conditionTypeValidated,
				Status:             metav1.ConditionFalse,
				Reason:             "ValidationFailed",
				Message:            "One or more VMs have validation errors",
				ObservedGeneration: plan.Generation,
			})
	} else {
		plan.Status.Phase = vmav1alpha1.PlanPhaseReady
		meta.SetStatusCondition(&plan.Status.Conditions,
			metav1.Condition{
				Type:   conditionTypeValidated,
				Status: metav1.ConditionTrue,
				Reason: "ValidationPassed",
				Message: fmt.Sprintf(
					"All %d VMs validated successfully",
					len(plan.Spec.VMs)),
				ObservedGeneration: plan.Generation,
			})
	}

	if err := r.Status().Update(ctx, plan); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Plan validation complete",
		"phase", plan.Status.Phase,
		"vmCount", len(plan.Spec.VMs))

	return ctrl.Result{}, nil
}

// setPlanError updates the plan status to Error phase with a condition.
func (r *PlanReconciler) setPlanError(
	ctx context.Context,
	plan *vmav1alpha1.MigrationPlan,
	reason, message string,
) (ctrl.Result, error) {
	plan.Status.Phase = vmav1alpha1.PlanPhaseError

	meta.SetStatusCondition(&plan.Status.Conditions, metav1.Condition{
		Type:               conditionTypeValidated,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: plan.Generation,
	})

	if err := r.Status().Update(ctx, plan); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vmav1alpha1.MigrationPlan{}).
		Named("plan").
		Complete(r)
}
