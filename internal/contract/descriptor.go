package contract

import (
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"gopkg.in/yaml.v3"
)

// ParseDescriptor decodes the carried-set projection from the exact raw
// descriptor bytes. It does not reserialize or normalize those bytes: the raw
// contract.md supplied to BuildCarriedSet is the digest input.
func ParseDescriptor(raw []byte) (Descriptor, error) {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return Descriptor{}, fmt.Errorf("contract: parse descriptor frontmatter: %w", err)
	}
	var descriptor Descriptor
	if err := yaml.Unmarshal(fm.YAML, &descriptor); err != nil {
		return Descriptor{}, fmt.Errorf("contract: decode descriptor: %w", err)
	}
	return descriptor, nil
}
