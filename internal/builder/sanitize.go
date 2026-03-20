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
	"fmt"
	"regexp"
	"strings"
)

const maxNameLength = 63

var (
	nonAlphanumRegex = regexp.MustCompile(`[^a-z0-9-]`)
	multiHyphenRegex = regexp.MustCompile(`-{2,}`)
)

// SanitizeName converts a Nutanix VM name to a valid Kubernetes DNS-1123
// label. It lowercases, replaces non-alphanumeric characters with hyphens,
// collapses runs of hyphens, trims leading/trailing hyphens, and truncates
// to 63 characters. When existingNames is non-nil and the result collides,
// a numeric suffix (-2, -3, ...) is appended.
func SanitizeName(name string, existingNames map[string]bool) string {
	s := strings.ToLower(name)

	// Replace common separators with hyphens.
	s = strings.NewReplacer(" ", "-", "_", "-", "(", "-", ")", "-").Replace(s)

	// Strip everything that isn't a lowercase letter, digit, or hyphen.
	s = nonAlphanumRegex.ReplaceAllString(s, "-")

	// Collapse multiple hyphens.
	s = multiHyphenRegex.ReplaceAllString(s, "-")

	// Trim leading/trailing hyphens.
	s = strings.Trim(s, "-")

	// Truncate.
	if len(s) > maxNameLength {
		s = strings.TrimRight(s[:maxNameLength], "-")
	}

	// Ensure non-empty.
	if s == "" {
		s = "vm"
	}

	if existingNames == nil || !existingNames[s] {
		return s
	}

	// Resolve collisions.
	for i := 2; ; i++ {
		suffix := fmt.Sprintf("-%d", i)
		candidate := s
		if len(candidate)+len(suffix) > maxNameLength {
			candidate = strings.TrimRight(
				candidate[:maxNameLength-len(suffix)], "-",
			)
		}
		candidate += suffix
		if !existingNames[candidate] {
			return candidate
		}
	}
}
