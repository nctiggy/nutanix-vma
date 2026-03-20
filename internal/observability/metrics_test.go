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
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestMigrationsTotal_IsRegistered(t *testing.T) {
	if MigrationsTotal == nil {
		t.Fatal("MigrationsTotal is nil")
	}
	// Verify the counter can be used with known labels
	c, err := MigrationsTotal.GetMetricWithLabelValues(
		"completed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil counter")
	}
}

func TestMigrationDurationSeconds_IsRegistered(t *testing.T) {
	if MigrationDurationSeconds == nil {
		t.Fatal("MigrationDurationSeconds is nil")
	}
	o, err := MigrationDurationSeconds.GetMetricWithLabelValues(
		"test-vm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o == nil {
		t.Fatal("expected non-nil observer")
	}
}

func TestDiskTransferBytes_IsRegistered(t *testing.T) {
	if DiskTransferBytes == nil {
		t.Fatal("DiskTransferBytes is nil")
	}
	c, err := DiskTransferBytes.GetMetricWithLabelValues(
		"test-vm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil counter")
	}
}

func TestActiveMigrations_IsRegistered(t *testing.T) {
	if ActiveMigrations == nil {
		t.Fatal("ActiveMigrations is nil")
	}
	// Verify Inc/Dec work
	ActiveMigrations.Inc()
	ActiveMigrations.Dec()
}

func TestMigrationsTotal_Increment(t *testing.T) {
	// Reset by getting the current value
	c, err := MigrationsTotal.GetMetricWithLabelValues(
		"test_status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := &dto.Metric{}
	if err := c.(prometheus.Metric).Write(m); err != nil {
		t.Fatalf("failed to write metric: %v", err)
	}
	before := m.GetCounter().GetValue()

	MigrationsTotal.WithLabelValues("test_status").Inc()

	if err := c.(prometheus.Metric).Write(m); err != nil {
		t.Fatalf("failed to write metric: %v", err)
	}
	after := m.GetCounter().GetValue()

	if after != before+1 {
		t.Errorf("expected counter to increment by 1, "+
			"got %f -> %f", before, after)
	}
}

func TestMigrationDurationSeconds_Observe(t *testing.T) {
	MigrationDurationSeconds.WithLabelValues(
		"observe-test").Observe(120.5)
	// No panic = success; histogram internals are opaque
}

func TestDiskTransferBytes_Add(t *testing.T) {
	c, err := DiskTransferBytes.GetMetricWithLabelValues(
		"bytes-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := &dto.Metric{}
	if err := c.(prometheus.Metric).Write(m); err != nil {
		t.Fatalf("failed to write metric: %v", err)
	}
	before := m.GetCounter().GetValue()

	DiskTransferBytes.WithLabelValues("bytes-test").Add(
		1024 * 1024 * 1024)

	if err := c.(prometheus.Metric).Write(m); err != nil {
		t.Fatalf("failed to write metric: %v", err)
	}
	after := m.GetCounter().GetValue()

	expected := before + 1024*1024*1024
	if after != expected {
		t.Errorf("expected %f, got %f", expected, after)
	}
}
