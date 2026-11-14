package plugins

import "testing"

func TestVersionSatisfiesSemverRanges(t *testing.T) {
	cases := []struct {
		version    string
		constraint string
		want       bool
	}{
		{"1.2.3", "^1.0.0", true},
		{"2.0.0", "^1.0.0", false},
		{"0.2.9", "^0.2.3", true},
		{"0.3.0", "^0.2.3", false},
		{"0.0.3", "^0.0.3", true},
		{"0.0.4", "^0.0.3", false},
		{"1.2.9", "~1.2.3", true},
		{"1.3.0", "~1.2.3", false},
		{"1.3.0", "~1", true},
		{"2.0.0", "~1", false},
		{"1.2.3", ">=1.0.0 <2.0.0", true},
		{"2.0.0", ">=1.0.0 <2.0.0", false},
		{"1.2.3", ">=1.0.0, <2.0.0", true},
		{"1.2.3", "1.2", true},
		{"1.3.0", "1.2", false},
		{"1.9.0", "1", true},
		{"2.0.0", "1", false},
		{"1.2.3", "=1.2.3", true},
		{"1.2.4", "=1.2.3", false},
		{"1.2.3", "*", true},
		{"1.2.3", ">=2.0.0 || <1.0.0", false},
		{"0.9.0", ">=2.0.0 || <1.0.0", true},
		{"1.0.0-rc.1", ">=1.0.0-rc.1 <1.0.0", true},
		{"1.0.0", ">=1.0.0-rc.1 <1.0.0", false},
	}
	for _, testCase := range cases {
		got, err := versionSatisfies(testCase.version, testCase.constraint)
		if err != nil {
			t.Fatalf("%s %s: %v", testCase.version, testCase.constraint, err)
		}
		if got != testCase.want {
			t.Fatalf("%s against %s = %v, want %v", testCase.version, testCase.constraint, got, testCase.want)
		}
	}
}

func TestVersionSatisfiesRejectsMalformedRanges(t *testing.T) {
	for _, constraint := range []string{"", ">=", "garbage", "^", "~1.2.3 ||"} {
		if _, err := versionSatisfies("1.2.3", constraint); err == nil {
			t.Fatalf("constraint %q must be rejected", constraint)
		}
	}
}

func TestCompareVersionsHandlesPrerelease(t *testing.T) {
	if compareVersions("1.0.0-beta", "1.0.0") >= 0 {
		t.Fatal("1.0.0-beta must be lower than 1.0.0")
	}
	if compareVersions("1.0.0", "1.0.0-rc.1") <= 0 {
		t.Fatal("1.0.0 must be greater than 1.0.0-rc.1")
	}
	if compareVersions("1.0.0-beta.2", "1.0.0-beta.11") >= 0 {
		t.Fatal("numeric prerelease identifiers must compare numerically")
	}
}

func TestRecoveryDependencyOrderAndCycle(t *testing.T) {
	base := Installed{ID: "example.base", Manifest: Manifest{ID: "example.base"}}
	dependent := Installed{ID: "example.dependent", Manifest: Manifest{
		ID: "example.dependent", Dependencies: []Dependency{{ID: "example.base", Version: "^1.0.0"}},
	}}
	ordered, failures := orderEnabledByDependencies([]Installed{dependent, base})
	if len(failures) != 0 || len(ordered) != 2 || ordered[0].ID != "example.base" || ordered[1].ID != "example.dependent" {
		t.Fatalf("recovery order = %#v failures=%v", ordered, failures)
	}
	left := Installed{ID: "example.left", Manifest: Manifest{ID: "example.left", Dependencies: []Dependency{{ID: "example.right", Version: "^1.0.0"}}}}
	right := Installed{ID: "example.right", Manifest: Manifest{ID: "example.right", Dependencies: []Dependency{{ID: "example.left", Version: "^1.0.0"}}}}
	ordered, failures = orderEnabledByDependencies([]Installed{left, right})
	if len(failures) == 0 || len(ordered) != 0 {
		t.Fatalf("cycle recovery order = %#v failures=%v", ordered, failures)
	}
}

func TestValidateManifestDependencyDeclarations(t *testing.T) {
	base := validTestManifest()
	base.Dependencies = []Dependency{{ID: "example.base", Version: "^1.0.0"}}
	if findings := ValidateManifest(base, true); hasErrorFinding(findings) {
		t.Fatalf("valid dependency rejected: %#v", findings)
	}

	cases := []struct {
		name string
		dep  Dependency
		code string
	}{
		{"invalid id", Dependency{ID: "Not Namespaced", Version: "^1.0.0"}, "invalid_dependency_id"},
		{"self dependency", Dependency{ID: "example.hello", Version: "^1.0.0"}, "self_dependency"},
		{"invalid range", Dependency{ID: "example.base", Version: "latest"}, "invalid_dependency_range"},
	}
	for _, testCase := range cases {
		manifest := validTestManifest()
		manifest.Dependencies = []Dependency{testCase.dep}
		findings := ValidateManifest(manifest, true)
		found := false
		for _, finding := range findings {
			if finding.Code == testCase.code && finding.Severity == "error" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: findings %#v do not contain %s", testCase.name, findings, testCase.code)
		}
	}

	duplicate := validTestManifest()
	duplicate.Dependencies = []Dependency{
		{ID: "example.base", Version: "^1.0.0"},
		{ID: "example.base", Version: "^2.0.0"},
	}
	foundDuplicate := false
	for _, finding := range ValidateManifest(duplicate, true) {
		if finding.Code == "duplicate_dependency" && finding.Severity == "error" {
			foundDuplicate = true
		}
	}
	if !foundDuplicate {
		t.Fatalf("duplicate dependency not rejected: %#v", ValidateManifest(duplicate, true))
	}
}
