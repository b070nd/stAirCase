package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Overlay is a path of the user's checkout that a command sees inside the root it runs in, read-only: a project's environment, such as
// its `vendor/` directory, which a checkout of a commit does not hold because the repository ignores it. Source is the absolute path in
// the user's checkout; Target is where it appears, relative to the root.
type Overlay struct{ Source, Target string }

// linkOverlays makes each overlay visible in root for an engine that cannot bind a path read-only: as a link whose target the engine
// already forbids writing (the macOS sandbox), or, with no sandbox, a link and nothing that stops a write (the command is recorded as not
// sandboxed). Engines not tested for read-only here refuse, so a check never gets write access to the user's dependencies by accident.
func linkOverlays(root, engine string, overlays []Overlay) error {
	switch engine {
	case "", "sandbox-exec": // no sandbox, or one whose writes are confined to root and its temp folder
	default:
		return fmt.Errorf("a declared environment is not supported with the %s sandbox yet: it cannot be guaranteed read-only there. Use the macOS sandbox, "+
			"--sandbox docker:image=<image>, or --sandbox off (checks then run without a sandbox and nothing stops them writing to the environment)", engine)
	}
	for _, o := range overlays {
		dest := filepath.Join(root, filepath.FromSlash(o.Target))
		if _, err := os.Lstat(dest); err == nil {
			return fmt.Errorf("the environment path %s is in the checkout already (it is tracked): an environment is for what the repository ignores", o.Target)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(o.Source, dest); err != nil {
			return err
		}
	}
	return nil
}

// OverlayRefusal says why a declared environment cannot be shown to commands run in mode on this machine, or "" when it can: the engine
// that would run them must be able to keep it read-only. It is asked when a run starts, so a run is refused before any work is done.
func OverlayRefusal(mode string) string {
	if mode == Off || mode == DockerPrefix || strings.HasPrefix(mode, DockerPrefix+":") {
		return "" // no sandbox says so; a container binds it read-only
	}
	probe, err := os.MkdirTemp("", "staircase-sandbox-probe-")
	if err != nil {
		return err.Error()
	}
	defer func() { _ = os.RemoveAll(probe) }()
	if _, name, err := sandboxWrapper(probe, probe, nil); err == nil && name != "sandbox-exec" {
		return fmt.Sprintf("a declared environment is not supported with the %s sandbox yet: it cannot be guaranteed read-only there; "+
			"use the macOS sandbox, --sandbox docker:image=<image>, or --sandbox off", name)
	}
	return ""
}
