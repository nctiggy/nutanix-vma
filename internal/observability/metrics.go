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

package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// MigrationsTotal counts the total number of completed migrations
	// partitioned by status (completed, failed, cancelled).
	MigrationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vma_migrations_total",
			Help: "Total number of VM migrations by status.",
		},
		[]string{"status"},
	)

	// MigrationDurationSeconds tracks the duration of migrations
	// from start to completion.
	MigrationDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "vma_migration_duration_seconds",
			Help: "Duration of VM migrations in seconds.",
			Buckets: prometheus.ExponentialBuckets(
				60, 2, 10), // 1m, 2m, 4m, ... ~17h
		},
		[]string{"vm"},
	)

	// DiskTransferBytes tracks cumulative disk bytes transferred
	// via CDI DataVolumes.
	DiskTransferBytes = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vma_disk_transfer_bytes",
			Help: "Cumulative disk bytes transferred during migration.",
		},
		[]string{"vm"},
	)

	// ActiveMigrations tracks the number of currently running
	// migration CRs.
	ActiveMigrations = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "vma_active_migrations",
			Help: "Number of currently active migration CRs.",
		},
	)
)

func init() {
	metrics.Registry.MustRegister(
		MigrationsTotal,
		MigrationDurationSeconds,
		DiskTransferBytes,
		ActiveMigrations,
	)
}
