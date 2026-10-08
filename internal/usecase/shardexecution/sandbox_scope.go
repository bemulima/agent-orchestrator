package shardexecution

import "github.com/bemulima/agent-orchestrator/internal/domain"

func sandboxLayerPaths(wp domain.WorkPackage, phase string) []string {
	if phase == "boundary assessment" {
		return []string{}
	}
	if phase == "semantic RED test-only" {
		return append([]string{}, wp.Verification.TestPaths...)
	}
	if phase == "implementation" {
		return withoutSandboxTests(wp.WriteScope.Allow, wp.Verification.TestPaths)
	}
	return append([]string{}, wp.WriteScope.Allow...)
}
func sandboxCompositionPaths(wp domain.CompositionWorkPackage, phase string) []string {
	if phase == "semantic RED test-only" {
		return append([]string{}, wp.Verification.TestPaths...)
	}
	if phase == "wiring implementation after semantic RED" {
		return withoutSandboxTests(wp.WriteScope.Allow, wp.Verification.TestPaths)
	}
	return append([]string{}, wp.WriteScope.Allow...)
}
func withoutSandboxTests(paths, tests []string) []string {
	result := []string{}
	for _, p := range paths {
		test := false
		for _, t := range tests {
			if p == t {
				test = true
				break
			}
		}
		if !test {
			result = append(result, p)
		}
	}
	return result
}
