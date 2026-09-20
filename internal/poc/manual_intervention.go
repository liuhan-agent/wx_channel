package poc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const manualProgressEnvironment = "TRENDRADAR_MANUAL_INTERVENTION_PROGRESS_PATH"

type manualProgressWriter struct {
	path       string
	sequence   int
	startedAt  time.Time
	deadlineAt time.Time
	reason     string
	mu         sync.Mutex
}

func newManualProgressWriter() *manualProgressWriter {
	path := os.Getenv(manualProgressEnvironment)
	if path == "" || !filepath.IsAbs(path) || filepath.Base(path) != "manual-intervention.json" {
		path = ""
	}
	writer := &manualProgressWriter{path: path}
	if data, err := os.ReadFile(path); err == nil {
		var prior struct {
			Sequence int `json:"sequence"`
		}
		if json.Unmarshal(data, &prior) == nil && prior.Sequence > 0 {
			writer.sequence = prior.Sequence
		}
	}
	return writer
}

func (w *manualProgressWriter) begin(reason string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sequence++
	w.startedAt = time.Now().UTC()
	w.deadlineAt = w.startedAt.Add(180 * time.Second)
	w.reason = reason
	w.writeLocked("waiting_for_human")
}

func (w *manualProgressWriter) finish(phase string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sequence > 0 {
		w.writeLocked(phase)
	}
}

func (w *manualProgressWriter) writeLocked(phase string) {
	if w.path == "" {
		return
	}
	payload := map[string]any{
		"schema_version":       "manual-intervention-progress/1",
		"sequence":             w.sequence,
		"phase":                phase,
		"platform":             "wechat_channels",
		"reason":               w.reason,
		"started_at":           w.startedAt.Format(time.RFC3339Nano),
		"deadline_at":          w.deadlineAt.Format(time.RFC3339Nano),
		"foreground_attempted": false,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(w.path), ".manual-intervention-*.tmp")
	if err != nil {
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return
	}
	if err = tmp.Close(); err == nil {
		_ = os.Rename(name, w.path)
	}
}

func progressReason(reason WaitReason) string {
	switch reason {
	case WaitLogin:
		return "login_required"
	case WaitVerification:
		return "verification_required"
	default:
		return "login_or_verification_required"
	}
}
