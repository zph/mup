package deploy

import "fmt"

// Variant represents a MongoDB distribution variant
type Variant int

const (
	// VariantMongo represents the official MongoDB distribution
	VariantMongo Variant = iota
	// VariantPercona represents Percona Server for MongoDB
	VariantPercona
)

// String returns the string representation of the variant
func (v Variant) String() string {
	switch v {
	case VariantMongo:
		return "mongo"
	case VariantPercona:
		return "percona"
	default:
		return "unknown"
	}
}

// ParseVariant parses a string into a Variant
func ParseVariant(s string) (Variant, error) {
	switch s {
	case "mongo", "":
		return VariantMongo, nil
	case "percona":
		return VariantPercona, nil
	default:
		return 0, fmt.Errorf("unknown variant: %s (expected 'mongo' or 'percona')", s)
	}
}
