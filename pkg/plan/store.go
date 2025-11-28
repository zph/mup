package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	mathrand "math/rand"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/oklog/ulid/v2"
)

// PlanStore manages plan persistence
// REQ-PES-021: Plan persistence with unique IDs
// REQ-PES-022: Atomic plan writes
// REQ-PES-023: Plan verification with SHA-256
type PlanStore struct {
	storageDir string // Base storage directory (~/.mup/storage)
}

// NewPlanStore creates a new plan store
func NewPlanStore(storageDir string) (*PlanStore, error) {
	if storageDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		storageDir = filepath.Join(homeDir, ".mup", "storage")
	}

	return &PlanStore{
		storageDir: storageDir,
	}, nil
}

// SavePlan saves a plan to storage with metadata and integrity check
// REQ-PES-021: Generates ULID (time-sortable) and timestamp if not set
// REQ-PES-022: Uses atomic write (write to temp, then rename)
// REQ-PES-023: Computes SHA-256 for verification
func (s *PlanStore) SavePlan(p *Plan) (string, error) {
	// Generate plan ID if not set (ULID for time-based sorting)
	if p.PlanID == "" {
		entropy := ulid.Monotonic(mathrand.New(mathrand.NewSource(time.Now().UnixNano())), 0)
		p.PlanID = ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
	}

	// Set created timestamp if not set
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}

	// Create plans directory
	plansDir := s.GetPlansDir(p.ClusterName)
	if err := os.MkdirAll(plansDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create plans directory: %w", err)
	}

	// Serialize plan to JSON
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to serialize plan: %w", err)
	}

	// Compute SHA-256 checksum
	hash := sha256.Sum256(data)
	checksum := hex.EncodeToString(hash[:])

	// Write plan file atomically (write to temp, then rename)
	planPath := s.GetPlanPath(p.ClusterName, p.PlanID)
	tempPath := planPath + ".tmp"

	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write plan file: %w", err)
	}

	if err := os.Rename(tempPath, planPath); err != nil {
		os.Remove(tempPath) // Clean up temp file
		return "", fmt.Errorf("failed to rename plan file: %w", err)
	}

	// Write checksum file
	checksumPath := planPath + ".sha256"
	if err := os.WriteFile(checksumPath, []byte(checksum), 0644); err != nil {
		// Log warning but don't fail - plan is already saved
		fmt.Printf("Warning: failed to write checksum file: %v\n", err)
	}

	return p.PlanID, nil
}

// LoadPlan loads a plan from storage
// REQ-PES-024: Loads plan by ID and cluster name
func (s *PlanStore) LoadPlan(clusterName, planID string) (*Plan, error) {
	planPath := s.GetPlanPath(clusterName, planID)

	// Check if plan exists
	if _, err := os.Stat(planPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("plan not found: %s", planID)
		}
		return nil, fmt.Errorf("failed to stat plan file: %w", err)
	}

	// Load plan
	plan, err := LoadFromFile(planPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load plan: %w", err)
	}

	return plan, nil
}

// VerifyPlan verifies the integrity of a saved plan
// REQ-PES-023: Verifies SHA-256 checksum matches
func (s *PlanStore) VerifyPlan(clusterName, planID string) (bool, error) {
	planPath := s.GetPlanPath(clusterName, planID)
	checksumPath := planPath + ".sha256"

	// Read plan file
	data, err := os.ReadFile(planPath)
	if err != nil {
		return false, fmt.Errorf("failed to read plan file: %w", err)
	}

	// Compute current checksum
	hash := sha256.Sum256(data)
	currentChecksum := hex.EncodeToString(hash[:])

	// Read stored checksum
	storedChecksumBytes, err := os.ReadFile(checksumPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No checksum file - return false but not an error
			return false, nil
		}
		return false, fmt.Errorf("failed to read checksum file: %w", err)
	}
	storedChecksum := string(storedChecksumBytes)

	// Compare checksums
	return currentChecksum == storedChecksum, nil
}

// ListPlans returns all plans for a cluster, sorted by creation time (newest first)
// REQ-PES-025: Lists all plans for a cluster
func (s *PlanStore) ListPlans(clusterName string) ([]PlanMetadata, error) {
	plansDir := s.GetPlansDir(clusterName)

	// Check if plans directory exists
	if _, err := os.Stat(plansDir); err != nil {
		if os.IsNotExist(err) {
			return []PlanMetadata{}, nil
		}
		return nil, fmt.Errorf("failed to stat plans directory: %w", err)
	}

	// Read plan files
	entries, err := os.ReadDir(plansDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read plans directory: %w", err)
	}

	var plans []PlanMetadata
	for _, entry := range entries {
		if entry.IsDir() || !isJSONFile(entry.Name()) {
			continue
		}

		// Extract plan ID from filename
		planID := entry.Name()[:len(entry.Name())-5] // Remove .json

		// Load plan metadata (without full plan content for efficiency)
		metadata, err := s.GetPlanMetadata(clusterName, planID)
		if err != nil {
			// Skip plans with errors
			fmt.Printf("Warning: failed to load plan metadata for %s: %v\n", planID, err)
			continue
		}

		plans = append(plans, metadata)
	}

	// Sort by creation time (newest first)
	sort.Slice(plans, func(i, j int) bool {
		return plans[i].CreatedAt.After(plans[j].CreatedAt)
	})

	return plans, nil
}

// GetPlanMetadata returns metadata about a plan without loading the full plan
// REQ-PES-026: Returns plan summary information
func (s *PlanStore) GetPlanMetadata(clusterName, planID string) (PlanMetadata, error) {
	planPath := s.GetPlanPath(clusterName, planID)

	// Read plan file
	file, err := os.Open(planPath)
	if err != nil {
		return PlanMetadata{}, fmt.Errorf("failed to open plan file: %w", err)
	}
	defer file.Close()

	// Parse only what we need for metadata
	var planData struct {
		PlanID      string    `json:"plan_id"`
		Operation   string    `json:"operation"`
		ClusterName string    `json:"cluster_name"`
		CreatedAt   time.Time `json:"created_at"`
		Version     string    `json:"version,omitempty"`
		Variant     string    `json:"variant,omitempty"`
		Validation  struct {
			Valid bool `json:"valid"`
		} `json:"validation"`
		Phases []struct {
			Name string `json:"name"`
		} `json:"phases"`
	}

	if err := json.NewDecoder(file).Decode(&planData); err != nil {
		return PlanMetadata{}, fmt.Errorf("failed to decode plan: %w", err)
	}

	// Get file info for size
	info, err := file.Stat()
	if err != nil {
		return PlanMetadata{}, fmt.Errorf("failed to stat plan file: %w", err)
	}

	// Extract phase names
	var phaseNames []string
	for _, phase := range planData.Phases {
		phaseNames = append(phaseNames, phase.Name)
	}

	// Check if verified
	verified, _ := s.VerifyPlan(clusterName, planID)

	return PlanMetadata{
		PlanID:      planData.PlanID,
		Operation:   planData.Operation,
		ClusterName: planData.ClusterName,
		CreatedAt:   planData.CreatedAt,
		Version:     planData.Version,
		Variant:     planData.Variant,
		IsValid:     planData.Validation.Valid,
		PhaseCount:  len(planData.Phases),
		PhaseNames:  phaseNames,
		SizeBytes:   info.Size(),
		Verified:    verified,
	}, nil
}

// DeletePlan removes a plan from storage
// REQ-PES-027: Removes plan and checksum files
func (s *PlanStore) DeletePlan(clusterName, planID string) error {
	planPath := s.GetPlanPath(clusterName, planID)
	checksumPath := planPath + ".sha256"

	// Remove plan file
	if err := os.Remove(planPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove plan file: %w", err)
	}

	// Remove checksum file (ignore errors)
	os.Remove(checksumPath)

	return nil
}

// GetPlansDir returns the plans directory for a cluster
func (s *PlanStore) GetPlansDir(clusterName string) string {
	return filepath.Join(s.storageDir, "clusters", clusterName, "plans")
}

// GetPlanPath returns the path to a plan file
func (s *PlanStore) GetPlanPath(clusterName, planID string) string {
	return filepath.Join(s.GetPlansDir(clusterName), fmt.Sprintf("%s.json", planID))
}

// PlanMetadata contains summary information about a plan
type PlanMetadata struct {
	PlanID      string    `json:"plan_id"`
	Operation   string    `json:"operation"`
	ClusterName string    `json:"cluster_name"`
	CreatedAt   time.Time `json:"created_at"`
	Version     string    `json:"version,omitempty"`
	Variant     string    `json:"variant,omitempty"`
	IsValid     bool      `json:"is_valid"`
	PhaseCount  int       `json:"phase_count"`
	PhaseNames  []string  `json:"phase_names"`
	SizeBytes   int64     `json:"size_bytes"`
	Verified    bool      `json:"verified"` // Whether SHA-256 checksum is valid
}

// ComputeChecksum computes the SHA-256 checksum of a file
func ComputeChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to compute checksum: %w", err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// isJSONFile checks if a filename has .json extension
func isJSONFile(name string) bool {
	return len(name) > 5 && name[len(name)-5:] == ".json"
}
