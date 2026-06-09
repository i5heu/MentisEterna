package server

import (
	"fmt"
	"math"
	"sync"
	"time"
)

const (
	loginThrottleFreeFailures = 3
	loginThrottleMaxDelay     = 30 * time.Second
	loginThrottleStateTTL     = 15 * time.Minute
)

type loginThrottle struct {
	mu       sync.Mutex
	now      func() time.Time
	username map[string]throttleState
	ip       map[string]throttleState
}

type throttleState struct {
	Failures     int
	BlockedUntil time.Time
	LastSeen     time.Time
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{
		now:      time.Now,
		username: make(map[string]throttleState),
		ip:       make(map[string]throttleState),
	}
}

func (l *loginThrottle) allow(username, ip string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.prune(now)

	if wait, blocked := remainingDelay(l.username[username], now); blocked {
		return wait, false
	}
	if wait, blocked := remainingDelay(l.ip[ip], now); blocked {
		return wait, false
	}
	return 0, true
}

func (l *loginThrottle) recordFailure(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.prune(now)
	l.username[username] = nextThrottleState(l.username[username], now)
	l.ip[ip] = nextThrottleState(l.ip[ip], now)
}

func (l *loginThrottle) recordSuccess(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.username, username)
	delete(l.ip, ip)
}

func (l *loginThrottle) prune(now time.Time) {
	for key, state := range l.username {
		if now.Sub(state.LastSeen) > loginThrottleStateTTL {
			delete(l.username, key)
		}
	}
	for key, state := range l.ip {
		if now.Sub(state.LastSeen) > loginThrottleStateTTL {
			delete(l.ip, key)
		}
	}
}

func nextThrottleState(state throttleState, now time.Time) throttleState {
	state.Failures++
	state.LastSeen = now
	if state.Failures > loginThrottleFreeFailures {
		exponent := state.Failures - loginThrottleFreeFailures - 1
		delaySeconds := math.Pow(2, float64(exponent))
		delay := time.Duration(delaySeconds) * time.Second
		if delay > loginThrottleMaxDelay {
			delay = loginThrottleMaxDelay
		}
		state.BlockedUntil = now.Add(delay)
	}
	return state
}

func remainingDelay(state throttleState, now time.Time) (time.Duration, bool) {
	if state.BlockedUntil.IsZero() || !now.Before(state.BlockedUntil) {
		return 0, false
	}
	return state.BlockedUntil.Sub(now), true
}

func throttleKeyUsername(username string) string {
	if username == "" {
		return "<empty>"
	}
	return username
}

func throttleKeyIP(ip string) string {
	if ip == "" {
		return "<unknown>"
	}
	return ip
}

func formatRetryAfter(wait time.Duration) string {
	seconds := int(math.Ceil(wait.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d", seconds)
}
