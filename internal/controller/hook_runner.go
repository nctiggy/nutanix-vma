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
	"encoding/json"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
)

const (
	hookMaxRetries          = 3
	hookConfigMapPath       = "/tmp/hook"
	hookAnnotationKey       = "vma.nutanix.io/hook-attempt"
	hookDefaultSvcAccount   = "default"
	hookDefaultDeadlineSecs = int64(600)
)

// hookContext contains the migration context data mounted into hook Jobs.
type hookContext struct {
	VM   hookVMContext   `json:"vm"`
	Plan hookPlanContext `json:"plan"`
}

type hookVMContext struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Phase      string `json:"phase"`
	PowerState string `json:"originalPowerState,omitempty"`
}

type hookPlanContext struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	TargetNamespace string `json:"targetNamespace"`
}

// runHook executes a hook Job for the given step (PreHook or PostHook).
// Returns a PhaseResult: Completed=true when the Job succeeds,
// Completed=false when the Job is still running, Error when it fails
// after max retries.
func (r *MigrationReconciler) runHook(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
	step string,
) PhaseResult {
	logger := log.FromContext(ctx)

	// Find the Hook ref for this VM and step
	hook, found := r.findHook(ctx, vmStatus, mctx, step)
	if !found {
		// No hook configured for this step -- skip
		return PhaseResult{Completed: true}
	}

	migName := mctx.Migration.Name
	jobName := fmt.Sprintf("vma-%s-%s-%s",
		shortID(migName), shortID(vmStatus.ID), hookStepSuffix(step))

	targetNS := mctx.Plan.Spec.TargetNamespace

	// Check if Job already exists
	existingJob := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{
		Name: jobName, Namespace: targetNS,
	}, existingJob)

	if err == nil {
		// Job exists -- check its status
		return r.checkHookJob(ctx, existingJob, hook,
			vmStatus, mctx, step, jobName, targetNS)
	}

	if !apierrors.IsNotFound(err) {
		return PhaseResult{Error: fmt.Errorf(
			"%s: get Job: %w", step, err)}
	}

	// Create ConfigMap with migration context
	cmName := jobName + "-ctx"
	if err := r.createHookConfigMap(ctx, cmName, targetNS,
		vmStatus, mctx); err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"%s: create ConfigMap: %w", step, err)}
	}

	// Create the Job
	if err := r.createHookJob(ctx, jobName, targetNS,
		hook, cmName, mctx); err != nil {
		return PhaseResult{Error: fmt.Errorf(
			"%s: create Job: %w", step, err)}
	}

	logger.Info("Hook Job created",
		"step", step, "job", jobName, "vm", vmStatus.ID)
	return PhaseResult{Completed: false}
}

// findHook resolves the Hook CR for a given VM and step.
func (r *MigrationReconciler) findHook(
	ctx context.Context,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
	step string,
) (*vmav1alpha1.Hook, bool) {
	// Find the PlanVM entry for this VM
	var planVM *vmav1alpha1.PlanVM
	for i := range mctx.Plan.Spec.VMs {
		if mctx.Plan.Spec.VMs[i].ID == vmStatus.ID {
			planVM = &mctx.Plan.Spec.VMs[i]
			break
		}
	}
	if planVM == nil || len(planVM.Hooks) == 0 {
		return nil, false
	}

	// Find the hook ref matching this step
	for _, hookRef := range planVM.Hooks {
		if hookRef.Step != step {
			continue
		}

		hook := &vmav1alpha1.Hook{}
		if err := r.Get(ctx, types.NamespacedName{
			Name:      hookRef.HookRef.Name,
			Namespace: mctx.Migration.Namespace,
		}, hook); err != nil {
			return nil, false
		}
		return hook, true
	}

	return nil, false
}

// checkHookJob examines an existing Job's status and decides next action.
func (r *MigrationReconciler) checkHookJob(
	ctx context.Context,
	job *batchv1.Job,
	hook *vmav1alpha1.Hook,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
	step, jobName, targetNS string,
) PhaseResult {
	logger := log.FromContext(ctx)

	// Check for success
	if job.Status.Succeeded > 0 {
		logger.Info("Hook Job succeeded",
			"step", step, "job", jobName, "vm", vmStatus.ID)
		// Clean up Job and ConfigMap (best-effort)
		r.cleanupHookResources(ctx, jobName, targetNS)
		return PhaseResult{Completed: true}
	}

	// Check for failure
	if job.Status.Failed > 0 {
		attempt := getHookAttempt(job)
		if attempt >= hookMaxRetries {
			logger.Info("Hook Job failed after max retries",
				"step", step, "job", jobName,
				"attempts", attempt, "vm", vmStatus.ID)
			r.cleanupHookResources(ctx, jobName, targetNS)
			return PhaseResult{Error: fmt.Errorf(
				"%s: Job %s failed after %d attempts",
				step, jobName, hookMaxRetries)}
		}

		// Retry: delete the failed Job and recreate
		logger.Info("Hook Job failed, retrying",
			"step", step, "job", jobName,
			"attempt", attempt+1, "vm", vmStatus.ID)

		// Delete existing Job
		propagation := metav1.DeletePropagationBackground
		if err := r.Delete(ctx, job, &client.DeleteOptions{
			PropagationPolicy: &propagation,
		}); err != nil && !apierrors.IsNotFound(err) {
			return PhaseResult{Error: fmt.Errorf(
				"%s: delete failed Job: %w", step, err)}
		}

		// ConfigMap already exists, just recreate the Job
		cmName := jobName + "-ctx"
		if err := r.createHookJobWithAttempt(ctx, jobName,
			targetNS, hook, cmName, mctx,
			attempt+1); err != nil {
			return PhaseResult{Error: fmt.Errorf(
				"%s: retry Job: %w", step, err)}
		}

		return PhaseResult{Completed: false}
	}

	// Still running
	return PhaseResult{Completed: false}
}

// createHookConfigMap creates a ConfigMap with migration context JSON.
func (r *MigrationReconciler) createHookConfigMap(
	ctx context.Context,
	name, namespace string,
	vmStatus *vmav1alpha1.VMMigrationStatus,
	mctx *migrationContext,
) error {
	hctx := hookContext{
		VM: hookVMContext{
			ID:         vmStatus.ID,
			Name:       vmStatus.Name,
			Phase:      string(vmStatus.Phase),
			PowerState: vmStatus.OriginalPowerState,
		},
		Plan: hookPlanContext{
			Name:            mctx.Plan.Name,
			Type:            string(mctx.Plan.Spec.Type),
			TargetNamespace: mctx.Plan.Spec.TargetNamespace,
		},
	}

	vmJSON, err := json.Marshal(hctx.VM)
	if err != nil {
		return fmt.Errorf("marshal VM context: %w", err)
	}
	planJSON, err := json.Marshal(hctx.Plan)
	if err != nil {
		return fmt.Errorf("marshal plan context: %w", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: vmav1alpha1.GroupVersion.String(),
				Kind:       "Migration",
				Name:       mctx.Migration.Name,
				UID:        mctx.Migration.UID,
			}},
		},
		Data: map[string]string{
			"vm.json":   string(vmJSON),
			"plan.json": string(planJSON),
		},
	}

	if err := r.Create(ctx, cm); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	return nil
}

// createHookJob creates a K8s Job from the Hook CR spec.
func (r *MigrationReconciler) createHookJob(
	ctx context.Context,
	name, namespace string,
	hook *vmav1alpha1.Hook,
	configMapName string,
	mctx *migrationContext,
) error {
	return r.createHookJobWithAttempt(ctx, name, namespace,
		hook, configMapName, mctx, 1)
}

// createHookJobWithAttempt creates a hook Job with a specific attempt number.
func (r *MigrationReconciler) createHookJobWithAttempt(
	ctx context.Context,
	name, namespace string,
	hook *vmav1alpha1.Hook,
	configMapName string,
	mctx *migrationContext,
	attempt int,
) error {
	deadline := parseDeadline(hook.Spec.Deadline)
	backoffLimit := int32(0) // No K8s-level retries; we handle retries ourselves

	volumes := []corev1.Volume{{
		Name: "hook-context",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: configMapName,
				},
			},
		},
	}}

	volumeMounts := []corev1.VolumeMount{{
		Name:      "hook-context",
		MountPath: hookConfigMapPath,
		ReadOnly:  true,
	}}

	// If playbook is set, add it as a volume
	if hook.Spec.Playbook != "" {
		volumes = append(volumes, corev1.Volume{
			Name: "hook-playbook",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: configMapName,
					},
				},
			},
		})
	}

	serviceAccount := hook.Spec.ServiceAccount
	if serviceAccount == "" {
		serviceAccount = hookDefaultSvcAccount
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"vma.nutanix.io/migration": mctx.Migration.Name,
				"vma.nutanix.io/hook":      hook.Name,
			},
			Annotations: map[string]string{
				hookAnnotationKey: fmt.Sprintf("%d", attempt),
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: vmav1alpha1.GroupVersion.String(),
				Kind:       "Migration",
				Name:       mctx.Migration.Name,
				UID:        mctx.Migration.UID,
			}},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:          &backoffLimit,
			ActiveDeadlineSeconds: &deadline,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ServiceAccountName: serviceAccount,
					RestartPolicy:      corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:         "hook",
						Image:        hook.Spec.Image,
						VolumeMounts: volumeMounts,
					}},
					Volumes: volumes,
				},
			},
		},
	}

	if err := r.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	return nil
}

// cleanupHookResources deletes a hook Job and its ConfigMap (best-effort).
func (r *MigrationReconciler) cleanupHookResources(
	ctx context.Context,
	jobName, namespace string,
) {
	logger := log.FromContext(ctx)

	// Delete Job with background propagation (deletes pods)
	propagation := metav1.DeletePropagationBackground
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: jobName, Namespace: namespace,
		},
	}
	if err := r.Delete(ctx, job, &client.DeleteOptions{
		PropagationPolicy: &propagation,
	}); err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "cleanup hook Job", "job", jobName)
	}

	// Delete ConfigMap
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: jobName + "-ctx", Namespace: namespace,
		},
	}
	if err := r.Delete(ctx, cm); err != nil &&
		!apierrors.IsNotFound(err) {
		logger.Error(err, "cleanup hook ConfigMap",
			"configmap", jobName+"-ctx")
	}
}

// getHookAttempt reads the attempt number from a Job's annotation.
func getHookAttempt(job *batchv1.Job) int {
	if job.Annotations == nil {
		return 1
	}
	val, ok := job.Annotations[hookAnnotationKey]
	if !ok {
		return 1
	}
	var attempt int
	if _, err := fmt.Sscanf(val, "%d", &attempt); err != nil {
		return 1
	}
	return attempt
}

// parseDeadline parses a duration string and returns seconds.
// Defaults to 600 (10 minutes) on parse failure.
func parseDeadline(s string) int64 {
	if s == "" {
		return hookDefaultDeadlineSecs
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return hookDefaultDeadlineSecs
	}
	return int64(d.Seconds())
}

// hookStepSuffix returns a short suffix for Job naming.
func hookStepSuffix(step string) string {
	if step == "PreHook" {
		return "pre"
	}
	return "post"
}
