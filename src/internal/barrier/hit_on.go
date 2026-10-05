//go:build barriers

package barrier

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	mu   sync.Mutex
	seen = map[string]int{}
)

var hook atomic.Pointer[func(string)]

// SetHook makes every point call f first, in the process's own goroutine, for a test that
// has to do something at an exact point (cancel a run, say) instead of killing a process
// there. It returns what restores the previous state.
func SetHook(f func(string)) (restore func()) {
	old := hook.Swap(&f)
	return func() { hook.Store(old) }
}

// Hit holds the process when STAIRCASE_BARRIER names this point (at its n-th visit for name@n).
func Hit(name string) {
	if f := hook.Load(); f != nil {
		(*f)(name)
	}
	want, dir := os.Getenv("STAIRCASE_BARRIER"), os.Getenv("STAIRCASE_BARRIER_DIR")
	if want == "" || dir == "" {
		return
	}
	target, nth := want, 1
	if i := strings.IndexByte(want, '@'); i >= 0 {
		target = want[:i]
		if n, err := strconv.Atoi(want[i+1:]); err == nil && n > 0 {
			nth = n
		}
	}
	if target != name {
		return
	}
	mu.Lock()
	seen[name]++
	reached := seen[name] == nth
	mu.Unlock()
	if !reached {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, name+".reached"), []byte(strconv.Itoa(os.Getpid())), 0o600)
	for {
		time.Sleep(time.Hour)
	}
}
