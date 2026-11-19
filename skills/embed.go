package skills

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// SkillMD is the raw embedded content of skills/mem/SKILL.md.
//
//go:embed mem/SKILL.md
var SkillMD []byte

// Frontmatter holds the parsed metadata from the top of SKILL.md.
type Frontmatter struct {
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	License       string            `json:"license,omitempty"`
	Compatibility string            `json:"compatibility,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

var nameRegex = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// SHA256 returns the hex-encoded SHA-256 digest of SkillMD.
func SHA256() string {
	sum := sha256.Sum256(SkillMD)
	return hex.EncodeToString(sum[:])
}

// ReadFrontmatter parses and validates the frontmatter of the embedded SkillMD.
func ReadFrontmatter() (Frontmatter, error) {
	fm, err := ParseFrontmatter(SkillMD)
	if err != nil {
		return Frontmatter{}, err
	}
	if err := fm.Validate("mem"); err != nil {
		return Frontmatter{}, err
	}
	return fm, nil
}

// ParseFrontmatter parses the YAML frontmatter from raw markdown content without external dependencies.
func ParseFrontmatter(data []byte) (Frontmatter, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return Frontmatter{}, fmt.Errorf("frontmatter must start with ---")
	}

	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			endIdx = i
			break
		}
	}
	if endIdx == -1 {
		return Frontmatter{}, fmt.Errorf("closing --- delimiter not found")
	}

	fmLines := lines[1:endIdx]
	fm := Frontmatter{
		Metadata: make(map[string]string),
	}

	inMetadata := false
	for lineNum, line := range fmLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if inMetadata {
			if strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") {
				subTrimmed := strings.TrimSpace(line)
				colonIdx := strings.Index(subTrimmed, ":")
				if colonIdx == -1 {
					return Frontmatter{}, fmt.Errorf("invalid metadata entry at line %d: %q", lineNum+2, line)
				}
				mKey := strings.TrimSpace(subTrimmed[:colonIdx])
				rawVal := strings.TrimSpace(subTrimmed[colonIdx+1:])
				val, isStr := parseYAMLString(rawVal)
				if !isStr {
					return Frontmatter{}, fmt.Errorf("metadata value for %q must be a string, got %q", mKey, rawVal)
				}
				fm.Metadata[mKey] = val
				continue
			}
			inMetadata = false
		}

		colonIdx := strings.Index(line, ":")
		if colonIdx == -1 {
			return Frontmatter{}, fmt.Errorf("invalid frontmatter line %d: %q", lineNum+2, line)
		}

		key := strings.TrimSpace(line[:colonIdx])
		val := strings.TrimSpace(line[colonIdx+1:])

		switch key {
		case "name":
			parsedVal, isStr := parseYAMLString(val)
			if !isStr {
				return Frontmatter{}, fmt.Errorf("name must be a string")
			}
			fm.Name = parsedVal
		case "description":
			parsedVal, isStr := parseYAMLString(val)
			if !isStr {
				return Frontmatter{}, fmt.Errorf("description must be a string")
			}
			fm.Description = parsedVal
		case "license":
			parsedVal, isStr := parseYAMLString(val)
			if !isStr {
				return Frontmatter{}, fmt.Errorf("license must be a string")
			}
			fm.License = parsedVal
		case "compatibility":
			parsedVal, isStr := parseYAMLString(val)
			if !isStr {
				return Frontmatter{}, fmt.Errorf("compatibility must be a string")
			}
			fm.Compatibility = parsedVal
		case "metadata":
			inMetadata = true
			if val != "" {
				return Frontmatter{}, fmt.Errorf("metadata must be a dictionary on subsequent indented lines")
			}
		default:
			return Frontmatter{}, fmt.Errorf("unexpected frontmatter key %q", key)
		}
	}

	return fm, nil
}

// parseYAMLString unquotes and validates a scalar string value.
func parseYAMLString(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", true
	}
	// Check for array or nested object notation
	if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{") {
		return "", false
	}
	// Quoted strings
	if (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) ||
		(strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`)) {
		if len(s) >= 2 {
			return s[1 : len(s)-1], true
		}
	}
	return s, true
}

// Validate validates the frontmatter against the Agent Skills specification.
func (f *Frontmatter) Validate(dirName string) error {
	if f.Name == "" {
		return fmt.Errorf("name is required")
	}
	if len(f.Name) > 64 {
		return fmt.Errorf("name exceeds maximum length of 64 characters (got %d)", len(f.Name))
	}
	if !nameRegex.MatchString(f.Name) {
		return fmt.Errorf("name %q is invalid: must consist of lowercase letters, digits, and single hyphens", f.Name)
	}
	if dirName != "" && f.Name != dirName {
		return fmt.Errorf("name %q must match directory name %q", f.Name, dirName)
	}

	if f.Description == "" {
		return fmt.Errorf("description is required and cannot be empty")
	}
	if len(f.Description) > 1024 {
		return fmt.Errorf("description exceeds maximum length of 1024 characters (got %d)", len(f.Description))
	}

	if len(f.Compatibility) > 500 {
		return fmt.Errorf("compatibility exceeds maximum length of 500 characters (got %d)", len(f.Compatibility))
	}

	for k, v := range f.Metadata {
		if k == "" {
			return fmt.Errorf("metadata key cannot be empty")
		}
		_ = v
	}

	return nil
}

// IsByteIdentical reports whether diskData matches SkillMD exactly.
func IsByteIdentical(diskData []byte) bool {
	return bytes.Equal(diskData, SkillMD)
}
