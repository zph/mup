package upgrade

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const binaryFileMode os.FileMode = 0o755

// reconcileWithExistingState aligns a requested upgrade with an unfinished
// one found on disk. A resume (no target given) takes its target and FCV
// choice from the saved state; a request for a different target is refused
// rather than silently continuing the old upgrade under a new name.
// UPGSAFE-009
func reconcileWithExistingState(config *UpgradeConfig, variant string, state *UpgradeState) error {
	prefix := variant + "-"
	savedTarget := strings.TrimPrefix(state.TargetVersion, prefix)
	if !strings.HasPrefix(state.TargetVersion, prefix) || savedTarget == "" {
		return fmt.Errorf("saved upgrade state has an unusable target version %q", state.TargetVersion)
	}

	if config.ToVersion == "" {
		config.ToVersion = savedTarget
	} else if config.ToVersion != savedTarget {
		return fmt.Errorf("an unfinished upgrade (%s) to %s already exists, but %s was requested; resume it with --resume or finish it before starting another",
			state.UpgradeID, savedTarget, config.ToVersion)
	}

	config.UpgradeFCV = config.UpgradeFCV || state.UpgradeFCV
	return nil
}

// canResumeFrom reports whether an upgrade in the given status can continue.
// An upgrade left in_progress was interrupted (crash or kill) and is resumable.
// UPGSAFE-009
func canResumeFrom(status OverallStatus) bool {
	return status == OverallStatusPaused || status == OverallStatusFailed || status == OverallStatusInProgress
}

// installBinary puts src at dst. If dst already has identical content it is
// left alone; otherwise the new content is written beside it and renamed into
// place, so a process executing the old file is never handed a truncated one.
// It reports whether dst was replaced.
// UPGSAFE-010
func installBinary(src, dst string) (replaced bool, err error) {
	same, err := sameContent(src, dst)
	if err != nil {
		return false, err
	}
	if same {
		return false, nil
	}

	source, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, source.Close()) }()

	temp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp*")
	if err != nil {
		return false, err
	}
	tempPath := temp.Name()
	cleanup := func(cause error) error {
		return errors.Join(cause, temp.Close(), os.Remove(tempPath))
	}

	if _, err := io.Copy(temp, source); err != nil {
		return false, cleanup(err)
	}
	if err := temp.Chmod(binaryFileMode); err != nil {
		return false, cleanup(err)
	}
	if err := temp.Sync(); err != nil {
		return false, cleanup(err)
	}
	if err := temp.Close(); err != nil {
		return false, errors.Join(err, os.Remove(tempPath))
	}
	if err := os.Rename(tempPath, dst); err != nil {
		return false, errors.Join(err, os.Remove(tempPath))
	}
	return true, nil
}

// sameContent reports whether dst exists with the same bytes as src.
func sameContent(src, dst string) (bool, error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return false, err
	}
	dstInfo, err := os.Stat(dst)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if srcInfo.Size() != dstInfo.Size() {
		return false, nil
	}

	srcData, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	dstData, err := os.ReadFile(dst)
	if err != nil {
		return false, err
	}
	return bytes.Equal(srcData, dstData), nil
}
