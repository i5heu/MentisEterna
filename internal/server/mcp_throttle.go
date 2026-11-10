package server

import (
	"sync"
	"time"
)

const (
	// mcpThrottleFreeFailures is the number of unauthenticated /mcp failures
	// tolerated per key before exponential backoff kicks in. Agents may make
	// honest mistakes (expired token, typo), so this is more lenient than the
	// login throttle.
	mcpThrottleFreeFailures = 10
	// mcpThrottleMaxDelay caps the block window per key.
	mcpThrottleMaxDelay = 60 * time.Second
)

// keyThrottle is a per-key exponential-backoff failure throttle. It shares
// the state machine with the login throttle: the first N failures are free,
// then each additional failure blocks the key with exponentially growing
// delays; an authenticated success clears the key's counter entirely.
//
// Used for unauthenticated /mcp attempts, keyed by client IP. Requests relayed
// through the MCP proxy tunnel have no RemoteAddr, so they all share the
// "<unknown>" bucket — deliberate: a proxy that relays a brute force is
// throttled as a single aggregate source, while the first successful relay
// lifts the block again.
type keyThrottle struct {
	mu   sync.Mutex
	now  func() time.Time
	keys map[string]throttleState
}

func newKeyThrottle() *keyThrottle {
	return &keyThrottle{
		now:  time.Now,
		keys: make(map[string]throttleState),
	}
}

// allow reports whether the key may attempt authentication now; when false,
// it returns how long the key stays blocked.
func (k *keyThrottle) allow(key string) (time.Duration, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()

	now := k.now()
	k.prune(now)

	if wait, blocked := remainingDelay(k.keys[key], now); blocked {
		return wait, false
	}
	return 0, true
}

// recordFailure counts a failed authentication attempt for the key.
func (k *keyThrottle) recordFailure(key string) {
	k.mu.Lock()
	defer k.mu.Unlock()

	now := k.now()
	k.prune(now)
	k.keys[key] = nextThrottleState(k.keys[key], now, mcpThrottleFreeFailures, mcpThrottleMaxDelay)
}

// recordSuccess clears the key's failure history after a successful
// authentication.
func (k *keyThrottle) recordSuccess(key string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.keys, key)
}

func (k *keyThrottle) prune(now time.Time) {
	for key, state := range k.keys {
		if now.Sub(state.LastSeen) > loginThrottleStateTTL {
			delete(k.keys, key)
		}
	}
}
