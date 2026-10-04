//go:build barriers

package barrier

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	mu   sync.Mutex
	seen = map[string]int{}
)

// Hit holds the process when STAIRCASE_BARRIER names this point (at its n-th visit for name@n).
func Hit(name string) {
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
