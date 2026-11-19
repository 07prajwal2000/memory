package skills_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"memory/skills"
)

// TestDriftCheck_RealSkillPasses verifies that the canonical skills/mem/SKILL.md passes all drift assertions.
func TestDriftCheck_RealSkillPasses(t *testing.T) {
	errs := checkDrift(skills.SkillMD)
	if len(errs) > 0 {
		for _, err := range errs {
			t.Errorf("drift error: %v", err)
		}
		t.Fatalf("expected 0 drift errors, got %d", len(errs))
	}
}

// TestDriftCheck_NegativeTests verifies that the drift checker catches inaccuracies and lies.
func TestDriftCheck_NegativeTests(t *testing.T) {
	original := string(skills.SkillMD)

	t.Run("CatchesMadeUpCommand", func(t *testing.T) {
		mutated := strings.Replace(original, "mem sync", "mem teleport-hyperspace", 1)
		errs := checkDrift([]byte(mutated))
		assertHasErrorContaining(t, errs, "teleport-hyperspace")
	})

	t.Run("CatchesMadeUpFlag", func(t *testing.T) {
		mutated := strings.Replace(original, "[--context C]", "[--quantum-flux]", 1)
		errs := checkDrift([]byte(mutated))
		assertHasErrorContaining(t, errs, "quantum-flux")
	})

	t.Run("CatchesRenamedMCPArgument", func(t *testing.T) {
		mutated := strings.Replace(original, "if_version", "if_wrong_version", -1)
		errs := checkDrift([]byte(mutated))
		assertHasErrorContaining(t, errs, "if_wrong_version")
	})

	t.Run("CatchesWrongExpiry", func(t *testing.T) {
		mutated := strings.Replace(original, "7 days by default", "14 days by default", 1)
		errs := checkDrift([]byte(mutated))
		assertHasErrorContaining(t, errs, "expiry claim 14 days")
	})

	t.Run("CatchesBrokenFrontmatter_UppercaseName", func(t *testing.T) {
		mutated := strings.Replace(original, "name: mem", "name: MemSkill", 1)
		errs := checkDrift([]byte(mutated))
		assertHasErrorContaining(t, errs, "frontmatter validation error")
	})

	t.Run("CatchesBrokenFrontmatter_MissingDescription", func(t *testing.T) {
		mutated := strings.Replace(original, "description: Persistent", "description: \"\"\n# Persistent", 1)
		errs := checkDrift([]byte(mutated))
		assertHasErrorContaining(t, errs, "description is required")
	})

	t.Run("CatchesBrokenFrontmatter_NonStringMetadata", func(t *testing.T) {
		mutated := strings.Replace(original, "version: \"0.1.0\"", "version: [1, 2, 3]", 1)
		errs := checkDrift([]byte(mutated))
		assertHasErrorContaining(t, errs, "must be a string")
	})

	t.Run("CatchesWrongPort", func(t *testing.T) {
		mutated := strings.Replace(original, "127.0.0.1:7777", "127.0.0.1:9999", 1)
		errs := checkDrift([]byte(mutated))
		assertHasErrorContaining(t, errs, "default dashboard port")
	})
}

// TestSkillMD_DiskByteIdentical verifies that embedded SkillMD matches the file on disk.
func TestSkillMD_DiskByteIdentical(t *testing.T) {
	diskBytes, err := os.ReadFile(filepath.Join("mem", "SKILL.md"))
	if err != nil {
		// When tests run from project root, path is skills/mem/SKILL.md
		diskBytes, err = os.ReadFile(filepath.Join("..", "skills", "mem", "SKILL.md"))
	}
	if err != nil {
		t.Fatalf("could not read SKILL.md from disk: %v", err)
	}

	if !bytes.Equal(diskBytes, skills.SkillMD) {
		t.Fatalf("embedded SkillMD does not match disk content (embedded=%d bytes, disk=%d bytes)",
			len(skills.SkillMD), len(diskBytes))
	}
}

// TestFrontmatter_ValidSpec verifies that the frontmatter fields strictly conform to the spec.
func TestFrontmatter_ValidSpec(t *testing.T) {
	fm, err := skills.ReadFrontmatter()
	if err != nil {
		t.Fatalf("ReadFrontmatter: %v", err)
	}

	if fm.Name != "mem" {
		t.Errorf("Name = %q, want 'mem'", fm.Name)
	}
	if len(fm.Description) == 0 || len(fm.Description) > 1024 {
		t.Errorf("invalid description length: %d", len(fm.Description))
	}
	if len(fm.Compatibility) > 500 {
		t.Errorf("invalid compatibility length: %d", len(fm.Compatibility))
	}
	if fm.Metadata["version"] != "0.1.0" {
		t.Errorf("Metadata['version'] = %q, want '0.1.0'", fm.Metadata["version"])
	}
}

func assertHasErrorContaining(t *testing.T, errs []error, substr string) {
	t.Helper()
	if len(errs) == 0 {
		t.Fatalf("expected error containing %q, got no errors", substr)
	}
	for _, e := range errs {
		if strings.Contains(e.Error(), substr) {
			return
		}
	}
	var msgs []string
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	t.Fatalf("expected error containing %q, but got: %s", substr, strings.Join(msgs, "; "))
}
