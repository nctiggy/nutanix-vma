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

package builder

import (
	"strings"
	"testing"
)

const testVMName = "my-vm"

func TestSanitizeName_Simple(t *testing.T) {
	got := SanitizeName(testVMName, nil)
	if got != testVMName {
		t.Errorf("expected %s, got %s", testVMName, got)
	}
}

func TestSanitizeName_Uppercase(t *testing.T) {
	got := SanitizeName("My-VM", nil)
	if got != testVMName {
		t.Errorf("expected %s, got %s", testVMName, got)
	}
}

func TestSanitizeName_Spaces(t *testing.T) {
	got := SanitizeName("My Linux VM", nil)
	if got != "my-linux-vm" {
		t.Errorf("expected my-linux-vm, got %s", got)
	}
}

func TestSanitizeName_Underscores(t *testing.T) {
	got := SanitizeName("my_linux_vm", nil)
	if got != "my-linux-vm" {
		t.Errorf("expected my-linux-vm, got %s", got)
	}
}

func TestSanitizeName_Parens(t *testing.T) {
	got := SanitizeName("vm (copy)", nil)
	if got != "vm-copy" {
		t.Errorf("expected vm-copy, got %s", got)
	}
}

func TestSanitizeName_Unicode(t *testing.T) {
	got := SanitizeName("Ünïcödé VM", nil)
	if got != "n-c-d-vm" {
		t.Errorf("expected n-c-d-vm, got %s", got)
	}
}

func TestSanitizeName_LeadingTrailingHyphens(t *testing.T) {
	got := SanitizeName("--vm--", nil)
	if got != "vm" {
		t.Errorf("expected vm, got %s", got)
	}
}

func TestSanitizeName_MultipleHyphens(t *testing.T) {
	got := SanitizeName("vm---test", nil)
	if got != "vm-test" {
		t.Errorf("expected vm-test, got %s", got)
	}
}

func TestSanitizeName_Empty(t *testing.T) {
	got := SanitizeName("", nil)
	if got != "vm" {
		t.Errorf("expected vm, got %s", got)
	}
}

func TestSanitizeName_OnlySpecialChars(t *testing.T) {
	got := SanitizeName("!!!@@@", nil)
	if got != "vm" {
		t.Errorf("expected vm, got %s", got)
	}
}

func TestSanitizeName_Truncation(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := SanitizeName(long, nil)
	if len(got) > maxNameLength {
		t.Errorf("expected max %d chars, got %d", maxNameLength, len(got))
	}
	if got != strings.Repeat("a", maxNameLength) {
		t.Errorf("expected %d a's, got %s", maxNameLength, got)
	}
}

func TestSanitizeName_TruncationTrimsTrailingHyphen(t *testing.T) {
	// 62 a's + hyphen at position 63 should trim the hyphen.
	name := strings.Repeat("a", 62) + "-b" + strings.Repeat("c", 10)
	got := SanitizeName(name, nil)
	if len(got) > maxNameLength {
		t.Errorf("expected max %d chars, got %d", maxNameLength, len(got))
	}
	// Should be 63 chars: 62 a's + "-" gets trimmed... let's check.
	// Actually: "aaa...a-bccccccccc" truncated to 63 = "aaa...a-bcccccc..."
	// The 63rd char might not be a hyphen. Let's just verify length.
	if got[len(got)-1] == '-' {
		t.Errorf("name should not end with hyphen: %s", got)
	}
}

func TestSanitizeName_NoCollision(t *testing.T) {
	existing := map[string]bool{"other-vm": true}
	got := SanitizeName(testVMName, existing)
	if got != testVMName {
		t.Errorf("expected %s, got %s", testVMName, got)
	}
}

func TestSanitizeName_Collision(t *testing.T) {
	existing := map[string]bool{testVMName: true}
	got := SanitizeName(testVMName, existing)
	if got != testVMName+"-2" {
		t.Errorf("expected %s-2, got %s", testVMName, got)
	}
}

func TestSanitizeName_MultipleCollisions(t *testing.T) {
	existing := map[string]bool{
		testVMName:        true,
		testVMName + "-2": true,
		testVMName + "-3": true,
	}
	got := SanitizeName(testVMName, existing)
	if got != testVMName+"-4" {
		t.Errorf("expected %s-4, got %s", testVMName, got)
	}
}

func TestSanitizeName_CollisionWithTruncation(t *testing.T) {
	long := strings.Repeat("a", 63)
	existing := map[string]bool{long: true}
	got := SanitizeName(long, existing)
	// Should be 61 a's + "-2" = 63 chars.
	expected := strings.Repeat("a", 61) + "-2"
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}
