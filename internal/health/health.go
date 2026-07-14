// Package health exposes liveness and readiness endpoints.
//
// Liveness ("am I running?") is deliberately dependency-free — a Postgres
// outage must not cause the orchestrator to kill an otherwise healthy gateway.
// Readiness ("can I serve?") runs the registered dependency checks.
package health

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"sync"
	"time"
)

// Check reports whether one dependency is usable. It must respect ctx and
// return promptly; Handler applies its own timeout regardless.
type Check func(ctx context.Context) error

// Checker collects the dependency checks for a single service.
type Checker struct {
	service string
	version string
	timeout time.Duration

	mu     sync.RWMutex
	checks map[string]Check
}

// New creates a Checker for a service. Checks run with a 2s budget.
func New(service, version string) *Checker {
	return &Checker{
		service: service,
		version: version,
		timeout: 2 * time.Second,
		checks:  make(map[string]Check),
	}
}

// Register adds a named dependency check. Registering the same name twice
// replaces the previous check.
func (c *Checker) Register(name string, check Check) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks[name] = check
}

// status is the JSON body returned by both endpoints.
type status struct {
	Status  string            `json:"status"` // ok | degraded
	Service string            `json:"service"`
	Version string            `json:"version,omitempty"`
	Checks  map[string]string `json:"checks,omitempty"` // name -> "ok" or the error
}

// Live handles GET /healthz. It answers 200 as long as the process can serve
// HTTP at all, and never touches a dependency.
func (c *Checker) Live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, status{
		Status:  "ok",
		Service: c.service,
		Version: c.version,
	})
}

// Ready handles GET /readyz. It runs every registered check concurrently and
// answers 503 if any of them fails, naming the ones that did.
func (c *Checker) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), c.timeout)
	defer cancel()

	c.mu.RLock()
	checks := make(map[string]Check, len(c.checks))
	maps.Copy(checks, c.checks)
	c.mu.RUnlock()

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results = make(map[string]string, len(checks))
		healthy = true
	)
	for name, check := range checks {
		wg.Go(func() {
			// A panicking check is a bug, but it must not take the process
			// down through the readiness probe of all things.
			result := "ok"
			if err := runCheck(ctx, check); err != nil {
				result = err.Error()
			}
			mu.Lock()
			defer mu.Unlock()
			results[name] = result
			if result != "ok" {
				healthy = false
			}
		})
	}
	wg.Wait()

	body := status{Status: "ok", Service: c.service, Version: c.version, Checks: results}
	code := http.StatusOK
	if !healthy {
		body.Status = "degraded"
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, body)
}

// Routes registers both endpoints on mux.
func (c *Checker) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", c.Live)
	mux.HandleFunc("GET /readyz", c.Ready)
}

func runCheck(ctx context.Context, check Check) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &panicError{value: r}
		}
	}()
	return check(ctx)
}

type panicError struct{ value any }

func (e *panicError) Error() string { return "check panicked" }

func writeJSON(w http.ResponseWriter, code int, body status) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
