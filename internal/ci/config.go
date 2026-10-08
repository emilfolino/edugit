// Package ci runs repository-defined jobs in rootless containers and records
// the results.
package ci

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// ConfigPath is where a repo declares its jobs.
const ConfigPath = ".edugit/ci.json"

const (
	maxJobs        = 5
	defaultTimeout = 5 * time.Minute
	maxTimeout     = 15 * time.Minute
	maxConfigBytes = 64 << 10
)

// Image names must start alphanumerically so they can never be read as a
// container runtime option.
var (
	imageRE   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:@-]{0,199}$`)
	jobNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,39}$`)
)

// Job is one validated CI job.
type Job struct {
	Name    string `json:"name"`
	Image   string `json:"image"`
	Run     string `json:"run"`
	Network bool   `json:"network,omitempty"`
	Timeout string `json:"timeout,omitempty"`
}

// Duration returns the job's timeout, defaulted and capped.
func (j Job) Duration() time.Duration {
	d, err := time.ParseDuration(j.Timeout)
	if err != nil || d <= 0 {
		return defaultTimeout
	}
	return min(d, maxTimeout)
}

// Config is the content of .edugit/ci.json.
type Config struct {
	Jobs []Job `json:"jobs"`
}

// Parse validates a ci.json document.
func Parse(data []byte) (Config, error) {
	if len(data) > maxConfigBytes {
		return Config{}, errors.New("ci config too large")
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse ci config: %w", err)
	}
	if len(c.Jobs) == 0 {
		return Config{}, errors.New("ci config has no jobs")
	}
	if len(c.Jobs) > maxJobs {
		return Config{}, fmt.Errorf("ci config has more than %d jobs", maxJobs)
	}
	seen := map[string]bool{}
	for i, j := range c.Jobs {
		switch {
		case !jobNameRE.MatchString(j.Name):
			return Config{}, fmt.Errorf("job %d: invalid name", i+1)
		case seen[j.Name]:
			return Config{}, fmt.Errorf("job %q is defined twice", j.Name)
		case !imageRE.MatchString(j.Image):
			return Config{}, fmt.Errorf("job %q: invalid image", j.Name)
		case j.Run == "":
			return Config{}, fmt.Errorf("job %q: empty run", j.Name)
		}
		if j.Timeout != "" {
			if d, err := time.ParseDuration(j.Timeout); err != nil || d <= 0 {
				return Config{}, fmt.Errorf("job %q: invalid timeout", j.Name)
			}
		}
		seen[j.Name] = true
	}
	return c, nil
}
