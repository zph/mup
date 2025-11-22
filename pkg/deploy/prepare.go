package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/zph/mup/pkg/executor"
)

// prepare implements Phase 2: Prepare
// - Pre-flight checks (connectivity, disk space, ports)
// - Download and cache MongoDB binaries
// - Extract binaries
func (d *Deployer) prepare(ctx context.Context) error {
	fmt.Println("Phase 2: Prepare")
	fmt.Println("================")

	// Step 1: Pre-flight checks
	if err := d.preflightChecks(ctx); err != nil {
		return fmt.Errorf("pre-flight checks failed: %w", err)
	}

	// Step 2: Prepare binaries
	if err := d.prepareBinaries(ctx); err != nil {
		return fmt.Errorf("binary preparation failed: %w", err)
	}

	fmt.Println("✓ Phase 2 complete: Environment prepared")
	return nil
}

// PreflightIssue represents a pre-flight check issue
type PreflightIssue struct {
	Severity string // "error", "warning"
	Message  string
	Node     string // host:port or host
}

// preflightChecks performs comprehensive pre-deployment validation
func (d *Deployer) preflightChecks(ctx context.Context) error {
	fmt.Println("Running pre-flight checks...")

	var allIssues []PreflightIssue
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Collect all unique hosts
	hosts := d.topology.GetAllHosts()

	// Check each host in parallel
	for _, host := range hosts {
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			executor := d.executors[h]
			issues := d.checkHost(h, executor)

			mu.Lock()
			allIssues = append(allIssues, issues...)
			mu.Unlock()
		}(host)
	}

	wg.Wait()

	// Separate errors from warnings
	var errors []PreflightIssue
	var warnings []PreflightIssue

	for _, issue := range allIssues {
		if issue.Severity == "error" {
			errors = append(errors, issue)
		} else {
			warnings = append(warnings, issue)
		}
	}

	// Display warnings
	if len(warnings) > 0 {
		fmt.Println("\n  Warnings:")
		for _, w := range warnings {
			fmt.Printf("    - %s: %s\n", w.Node, w.Message)
		}
	}

	// Fail on errors
	if len(errors) > 0 {
		fmt.Println("\n  Errors:")
		for _, e := range errors {
			fmt.Printf("    ✗ %s: %s\n", e.Node, e.Message)
		}
		return fmt.Errorf("pre-flight checks failed with %d error(s)", len(errors))
	}

	fmt.Println("  ✓ Pre-flight checks passed")
	return nil
}

// checkHost performs all checks for a single host
func (d *Deployer) checkHost(host string, exec executor.Executor) []PreflightIssue {
	issues := []PreflightIssue{}

	// 1. Connectivity check
	if err := exec.CheckConnectivity(); err != nil {
		issues = append(issues, PreflightIssue{
			Severity: "error",
			Message:  fmt.Sprintf("Cannot connect: %v", err),
			Node:     host,
		})
		return issues // Fatal, can't continue
	}

	// 2. OS information
	osInfo, err := exec.GetOSInfo()
	if err != nil {
		issues = append(issues, PreflightIssue{
			Severity: "warning",
			Message:  fmt.Sprintf("Cannot get OS info: %v", err),
			Node:     host,
		})
	} else {
		fmt.Printf("    %s: OS=%s/%s\n", host, osInfo.OS, osInfo.Arch)
	}

	// 3. Disk space check
	checkPath := d.topology.Global.DataDir
	if d.isLocal {
		// For local, check home directory
		homeDir, err := os.UserHomeDir()
		if err != nil {
			issues = append(issues, PreflightIssue{
				Severity: "warning",
				Message:  fmt.Sprintf("Cannot get home directory: %v", err),
				Node:     host,
			})
		} else {
			checkPath = homeDir
		}
	}

	if checkPath != "" {
		available, err := exec.GetDiskSpace(checkPath)
		if err != nil {
			issues = append(issues, PreflightIssue{
				Severity: "warning",
				Message:  fmt.Sprintf("Cannot check disk space: %v", err),
				Node:     host,
			})
		} else {
			const minDiskSpace = 10 * 1024 * 1024 * 1024 // 10GB
			if available < minDiskSpace {
				issues = append(issues, PreflightIssue{
					Severity: "error",
					Message:  fmt.Sprintf("Insufficient disk space: %d GB available, %d GB required",
						available/(1024*1024*1024), minDiskSpace/(1024*1024*1024)),
					Node:     host,
				})
			} else {
				fmt.Printf("    %s: Disk space OK (%d GB available)\n",
					host, available/(1024*1024*1024))
			}
		}
	}

	// 4. Port availability check
	portsToCheck := d.getPortsForHost(host)
	for _, port := range portsToCheck {
		available, err := exec.CheckPortAvailable(port)
		if err != nil {
			issues = append(issues, PreflightIssue{
				Severity: "warning",
				Message:  fmt.Sprintf("Cannot check port %d: %v", port, err),
				Node:     fmt.Sprintf("%s:%d", host, port),
			})
		} else if !available {
			issues = append(issues, PreflightIssue{
				Severity: "error",
				Message:  fmt.Sprintf("Port %d already in use", port),
				Node:     fmt.Sprintf("%s:%d", host, port),
			})
		}
	}
	if len(portsToCheck) > 0 {
		fmt.Printf("    %s: Ports available: %v\n", host, portsToCheck)
	}

	// 5. User existence check (for remote deployment)
	if !d.isLocal {
		user := d.topology.Global.User
		if user != "" {
			exists, err := exec.UserExists(user)
			if err != nil {
				issues = append(issues, PreflightIssue{
					Severity: "warning",
					Message:  fmt.Sprintf("Cannot check user %s: %v", user, err),
					Node:     host,
				})
			} else if !exists {
				issues = append(issues, PreflightIssue{
					Severity: "warning",
					Message:  fmt.Sprintf("User %s does not exist, will be created", user),
					Node:     host,
				})
			}
		}
	}

	return issues
}

// getPortsForHost collects all ports used by nodes on a specific host
func (d *Deployer) getPortsForHost(host string) []int {
	ports := []int{}

	for _, node := range d.topology.Mongod {
		if node.Host == host {
			ports = append(ports, node.Port)
		}
	}
	for _, node := range d.topology.Mongos {
		if node.Host == host {
			ports = append(ports, node.Port)
		}
	}
	for _, node := range d.topology.ConfigSvr {
		if node.Host == host {
			ports = append(ports, node.Port)
		}
	}

	return ports
}

// prepareBinaries ensures MongoDB binaries are available for all required platforms
func (d *Deployer) prepareBinaries(ctx context.Context) error {
	fmt.Printf("Preparing MongoDB binaries (variant: %s, version: %s)...\n", d.variant.String(), d.version)

	// Create binary manager
	bm, err := NewBinaryManager()
	if err != nil {
		return fmt.Errorf("failed to create binary manager: %w", err)
	}
	defer bm.Close()

	// Collect all unique platforms from hosts
	platforms, err := d.CollectPlatforms(ctx)
	if err != nil {
		return fmt.Errorf("failed to collect platforms: %w", err)
	}

	// If no platforms collected (e.g., all local), use current platform
	if len(platforms) == 0 {
		currentPlatform := Platform{
			OS:   runtime.GOOS,
			Arch: runtime.GOARCH,
		}
		platforms = map[Platform]bool{currentPlatform: true}
	}

	// Fetch binaries for each platform
	binPaths := make(map[string]string) // platformKey -> binPath
	var mu sync.Mutex
	var wg sync.WaitGroup

	for platform := range platforms {
		wg.Add(1)
		go func(p Platform) {
			defer wg.Done()
			binPath, err := bm.GetBinPathWithVariant(d.version, d.variant, p)
			if err != nil {
				// Log error but continue with other platforms
				fmt.Printf("  Warning: failed to get binaries for %s %s: %v\n", d.variant, p.Key(), err)
				return
			}

			mu.Lock()
			binPaths[p.Key()] = binPath
			mu.Unlock()
		}(platform)
	}

	wg.Wait()

	// For local deployment, use the current platform's bin path
	if d.isLocal {
		currentPlatform := Platform{
			OS:   runtime.GOOS,
			Arch: runtime.GOARCH,
		}
		if binPath, ok := binPaths[currentPlatform.Key()]; ok {
			d.binPath = binPath
		} else {
			// If not found, try to fetch it
			binPath, err := bm.GetBinPathWithVariant(d.version, d.variant, currentPlatform)
			if err != nil {
				// Check if this is Percona on macOS - not supported
				if d.variant == VariantPercona && runtime.GOOS == "darwin" {
					return fmt.Errorf("Percona Server for MongoDB does not provide macOS binaries. Please use:\n  - Official MongoDB (--variant mongo) on macOS, or\n  - Deploy to a Linux host for Percona support")
				}

				// On macOS arm64, fall back to x86_64 (Rosetta 2 compatibility)
				if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
					fmt.Printf("  Note: %s %s not available for arm64, falling back to x86_64 (Rosetta 2)\n", d.variant.String(), d.version)
					fallbackPlatform := Platform{
						OS:   "darwin",
						Arch: "amd64",
					}
					binPath, err = bm.GetBinPathWithVariant(d.version, d.variant, fallbackPlatform)
					if err != nil {
						return fmt.Errorf("failed to ensure %s %s for current platform (tried arm64 and x86_64 fallback): %w", d.variant.String(), d.version, err)
					}
					d.binPath = binPath
				} else {
					return fmt.Errorf("failed to ensure %s %s for current platform: %w", d.variant.String(), d.version, err)
				}
			} else {
				d.binPath = binPath
			}
		}
	} else {
		// For remote deployment, store all platform bin paths
		// We'll use these in Phase 3 to distribute the correct binaries to each host
		// For now, store the first one as default
		for _, binPath := range binPaths {
			d.binPath = binPath
			break
		}
	}

	// Verify mongod binary exists
	if d.binPath != "" {
		mongodPath := filepath.Join(d.binPath, "mongod")
		if runtime.GOOS == "windows" {
			mongodPath = filepath.Join(d.binPath, "mongod.exe")
		}
		if _, err := os.Stat(mongodPath); err != nil {
			return fmt.Errorf("mongod binary not found at %s: %w", mongodPath, err)
		}
		fmt.Printf("  ✓ MongoDB %s binaries ready\n", d.version)
		for platformKey, binPath := range binPaths {
			fmt.Printf("    - %s: %s\n", platformKey, binPath)
		}
	}

	return nil
}

