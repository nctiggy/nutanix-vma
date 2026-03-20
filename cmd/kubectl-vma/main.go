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
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vmav1alpha1 "github.com/nctiggy/nutanix-vma/api/v1alpha1"
	"github.com/nctiggy/nutanix-vma/internal/nutanix"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var namespace string

	root := &cobra.Command{
		Use:   "kubectl-vma",
		Short: "kubectl plugin for Nutanix VM migration",
		Long: "kubectl-vma provides CLI access to the Nutanix VMA " +
			"operator for managing VM migrations to KubeVirt.",
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVarP(
		&namespace, "namespace", "n", "default", "Kubernetes namespace",
	)

	root.AddCommand(
		newInventoryCmd(&namespace),
		newPlanCmd(&namespace),
		newMigrateCmd(&namespace),
		newStatusCmd(&namespace),
		newCancelCmd(&namespace),
	)

	return root
}

// buildClients creates the controller-runtime client and clientset.
func buildClients() (client.Client, kubernetes.Interface, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, configOverrides,
	)
	restConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("loading kubeconfig: %w", err)
	}

	s := runtime.NewScheme()
	if err := vmav1alpha1.AddToScheme(s); err != nil {
		return nil, nil, fmt.Errorf("adding VMA scheme: %w", err)
	}

	c, err := client.New(restConfig, client.Options{Scheme: s})
	if err != nil {
		return nil, nil, fmt.Errorf("creating client: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("creating clientset: %w", err)
	}

	return c, clientset, nil
}

// --- inventory command ---

func newInventoryCmd(namespace *string) *cobra.Command {
	return &cobra.Command{
		Use:   "inventory <provider-name>",
		Short: "Display VM inventory from a Nutanix provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, clientset, err := buildClients()
			if err != nil {
				return err
			}
			return runInventory(
				cmd.Context(), c, clientset,
				*namespace, args[0], os.Stdout,
			)
		},
	}
}

func runInventory(
	ctx context.Context, c client.Client,
	clientset kubernetes.Interface,
	namespace, providerName string, out io.Writer,
) error {
	var provider vmav1alpha1.NutanixProvider
	key := types.NamespacedName{
		Name: providerName, Namespace: namespace,
	}
	if err := c.Get(ctx, key, &provider); err != nil {
		return fmt.Errorf("getting provider %q: %w", providerName, err)
	}

	secret, err := clientset.CoreV1().Secrets(namespace).Get(
		ctx, provider.Spec.SecretRef.Name, metav1.GetOptions{},
	)
	if err != nil {
		return fmt.Errorf(
			"getting secret %q: %w", provider.Spec.SecretRef.Name, err,
		)
	}

	nxClient, err := nutanix.NewClient(nutanix.ClientConfig{
		Host:               provider.Spec.URL,
		Username:           string(secret.Data["username"]),
		Password:           string(secret.Data["password"]),
		InsecureSkipVerify: provider.Spec.InsecureSkipVerify,
	})
	if err != nil {
		return fmt.Errorf("creating Nutanix client: %w", err)
	}

	vms, err := nxClient.ListVMs(ctx)
	if err != nil {
		return fmt.Errorf("listing VMs: %w", err)
	}

	printVMInventory(vms, out)
	return nil
}

func printVMInventory(vms []nutanix.VM, out io.Writer) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w,
		"UUID\tNAME\tCPU\tMEMORY (MiB)\tDISKS\tPOWER STATE")
	for _, vm := range vms {
		cpus := vm.NumSockets * vm.NumCoresPerSocket
		memMiB := vm.MemorySizeBytes / (1024 * 1024)
		diskCount := countDataDisks(vm.Disks)
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%s\n",
			vm.ExtID, vm.Name, cpus, memMiB, diskCount, vm.PowerState)
	}
	_ = w.Flush()
}

func countDataDisks(disks []nutanix.Disk) int {
	count := 0
	for _, d := range disks {
		if !strings.EqualFold(d.DeviceType, "CDROM") {
			count++
		}
	}
	return count
}

// --- plan command ---

func newPlanCmd(namespace *string) *cobra.Command {
	return &cobra.Command{
		Use:   "plan <plan-name>",
		Short: "Display migration plan status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClients()
			if err != nil {
				return err
			}
			return runPlan(
				cmd.Context(), c, *namespace, args[0], os.Stdout,
			)
		},
	}
}

func runPlan(
	ctx context.Context, c client.Client,
	namespace, planName string, out io.Writer,
) error {
	var plan vmav1alpha1.MigrationPlan
	key := types.NamespacedName{Name: planName, Namespace: namespace}
	if err := c.Get(ctx, key, &plan); err != nil {
		return fmt.Errorf("getting plan %q: %w", planName, err)
	}

	printPlanStatus(&plan, out)
	return nil
}

func printPlanStatus(plan *vmav1alpha1.MigrationPlan, out io.Writer) {
	_, _ = fmt.Fprintf(out, "Plan:       %s\n", plan.Name)
	_, _ = fmt.Fprintf(out, "Namespace:  %s\n", plan.Namespace)
	_, _ = fmt.Fprintf(out, "Type:       %s\n", plan.Spec.Type)
	_, _ = fmt.Fprintf(out, "Phase:      %s\n", plan.Status.Phase)
	_, _ = fmt.Fprintf(out, "Provider:   %s\n", plan.Spec.ProviderRef.Name)
	_, _ = fmt.Fprintf(out, "Target NS:  %s\n", plan.Spec.TargetNamespace)
	_, _ = fmt.Fprintf(out, "VMs:        %d\n", len(plan.Spec.VMs))
	_, _ = fmt.Fprintln(out)

	if len(plan.Status.VMs) > 0 {
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "VM UUID\tNAME\tCONCERNS")
		for _, vm := range plan.Status.VMs {
			concerns := formatConcerns(vm.Concerns)
			name := vm.Name
			if name == "" {
				name = "-"
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n",
				vm.ID, name, concerns)
		}
		_ = w.Flush()
	}
}

func formatConcerns(concerns []vmav1alpha1.Concern) string {
	if len(concerns) == 0 {
		return "None"
	}
	parts := make([]string, len(concerns))
	for i, c := range concerns {
		parts[i] = fmt.Sprintf("[%s] %s", c.Category, c.Message)
	}
	return strings.Join(parts, "; ")
}

// --- migrate command ---

func newMigrateCmd(namespace *string) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate <plan-name>",
		Short: "Create a Migration from a plan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClients()
			if err != nil {
				return err
			}
			return runMigrate(
				cmd.Context(), c, *namespace, args[0], os.Stdout,
			)
		},
	}
}

func runMigrate(
	ctx context.Context, c client.Client,
	namespace, planName string, out io.Writer,
) error {
	var plan vmav1alpha1.MigrationPlan
	key := types.NamespacedName{Name: planName, Namespace: namespace}
	if err := c.Get(ctx, key, &plan); err != nil {
		return fmt.Errorf("getting plan %q: %w", planName, err)
	}

	if plan.Status.Phase != vmav1alpha1.PlanPhaseReady {
		return fmt.Errorf(
			"plan %q is not Ready (current phase: %s)",
			planName, plan.Status.Phase,
		)
	}

	migrationName := fmt.Sprintf("%s-%d", planName, time.Now().Unix())

	migration := &vmav1alpha1.Migration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      migrationName,
			Namespace: namespace,
		},
		Spec: vmav1alpha1.MigrationSpec{
			PlanRef: corev1.LocalObjectReference{Name: planName},
		},
	}

	if err := c.Create(ctx, migration); err != nil {
		return fmt.Errorf("creating migration: %w", err)
	}

	_, _ = fmt.Fprintf(out,
		"Migration %q created from plan %q\n", migrationName, planName)
	return nil
}

// --- status command ---

func newStatusCmd(namespace *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status <migration-name>",
		Short: "Display migration status with per-VM progress",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClients()
			if err != nil {
				return err
			}
			return runStatus(
				cmd.Context(), c, *namespace, args[0], os.Stdout,
			)
		},
	}
}

func runStatus(
	ctx context.Context, c client.Client,
	namespace, migrationName string, out io.Writer,
) error {
	var migration vmav1alpha1.Migration
	key := types.NamespacedName{
		Name: migrationName, Namespace: namespace,
	}
	if err := c.Get(ctx, key, &migration); err != nil {
		return fmt.Errorf(
			"getting migration %q: %w", migrationName, err,
		)
	}

	printMigrationStatus(&migration, out)
	return nil
}

func printMigrationStatus(
	migration *vmav1alpha1.Migration, out io.Writer,
) {
	_, _ = fmt.Fprintf(out, "Migration:  %s\n", migration.Name)
	_, _ = fmt.Fprintf(out, "Namespace:  %s\n", migration.Namespace)
	_, _ = fmt.Fprintf(out,
		"Plan:       %s\n", migration.Spec.PlanRef.Name)
	_, _ = fmt.Fprintf(out,
		"Phase:      %s\n", migration.Status.Phase)
	if migration.Status.Started != nil {
		_, _ = fmt.Fprintf(out, "Started:    %s\n",
			migration.Status.Started.Format(time.RFC3339))
	}
	if migration.Status.Completed != nil {
		_, _ = fmt.Fprintf(out, "Completed:  %s\n",
			migration.Status.Completed.Format(time.RFC3339))
	}
	_, _ = fmt.Fprintln(out)

	if len(migration.Status.VMs) > 0 {
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w,
			"VM UUID\tNAME\tPHASE\tSTARTED\tCOMPLETED\tERROR")
		for _, vm := range migration.Status.VMs {
			name := vm.Name
			if name == "" {
				name = "-"
			}
			started := "-"
			if vm.Started != nil {
				started = vm.Started.Format(time.RFC3339)
			}
			completed := "-"
			if vm.Completed != nil {
				completed = vm.Completed.Format(time.RFC3339)
			}
			errMsg := "-"
			if vm.Error != "" {
				errMsg = vm.Error
			}
			_, _ = fmt.Fprintf(w,
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				vm.ID, name, vm.Phase,
				started, completed, errMsg)
		}
		_ = w.Flush()
	}
}

// --- cancel command ---

func newCancelCmd(namespace *string) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <migration-name> <vm-uuid> [<vm-uuid>...]",
		Short: "Cancel migration for specific VMs",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClients()
			if err != nil {
				return err
			}
			return runCancel(
				cmd.Context(), c,
				*namespace, args[0], args[1:], os.Stdout,
			)
		},
	}
}

func runCancel(
	ctx context.Context, c client.Client,
	namespace, migrationName string,
	vmIDs []string, out io.Writer,
) error {
	var migration vmav1alpha1.Migration
	key := types.NamespacedName{
		Name: migrationName, Namespace: namespace,
	}
	if err := c.Get(ctx, key, &migration); err != nil {
		return fmt.Errorf(
			"getting migration %q: %w", migrationName, err,
		)
	}

	existing := make(map[string]bool, len(migration.Spec.Cancel))
	for _, id := range migration.Spec.Cancel {
		existing[id] = true
	}
	added := 0
	for _, id := range vmIDs {
		if !existing[id] {
			migration.Spec.Cancel = append(
				migration.Spec.Cancel, id,
			)
			existing[id] = true
			added++
		}
	}

	if added == 0 {
		_, _ = fmt.Fprintf(out,
			"All specified VMs are already in the cancel list\n")
		return nil
	}

	if err := c.Update(ctx, &migration); err != nil {
		return fmt.Errorf("updating migration: %w", err)
	}

	_, _ = fmt.Fprintf(out,
		"Cancellation requested for %d VM(s) in migration %q\n",
		added, migrationName)
	return nil
}
