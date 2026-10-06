package store

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/lechandonga/genome-annotation-index-go/graph"
	"github.com/lechandonga/genome-annotation-index-go/model"
)

// EventType tags durable log records.
type EventType string

const (
	EventCommit   EventType = "commit"
	EventRollback EventType = "rollback"
)

// Event is one durable history record.
type Event struct {
	Type       EventType       `json:"type"`
	Revision   graph.Revision  `json:"revision"`
	ToRevision graph.Revision  `json:"to_revision,omitempty"`
	Source     string          `json:"source,omitempty"`
	OnConflict string          `json:"on_conflict,omitempty"`
	Features   []model.Feature `json:"features,omitempty"`
	Checksum   string          `json:"checksum"`
}

type envelope struct {
	Payload  json.RawMessage `json:"payload"`
	Checksum string          `json:"checksum"`
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// verifyLine validates a raw log line. The checksum covers the exact payload
// bytes, so corruption anywhere — including structurally ignored JSON tokens
// — is detected.
func verifyLine(raw []byte) (*Event, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if hashBytes(env.Payload) != env.Checksum {
		return nil, fmt.Errorf("event checksum mismatch")
	}
	var e Event
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return nil, err
	}
	e.Checksum = env.Checksum
	return &e, nil
}

func eventsPath(dir string) string   { return filepath.Join(dir, "events.jsonl") }
func lockPath(dir string) string     { return filepath.Join(dir, "lock") }
func manifestPath(dir string) string { return filepath.Join(dir, "manifest.json") }

// LoadEvents reads the append-only log. A truncated final record (e.g. crash
// mid-write) is reported separately as truncated so callers can safely
// rebuild from the intact prefix; a checksum failure of a complete record is
// a hard error because it indicates corruption.
func LoadEvents(dir string) (events []Event, truncated bool, err error) {
	f, err := os.Open(eventsPath(dir))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	br := bufio.NewReader(f)
	lineNo := 0
	for {
		raw, rerr := br.ReadBytes('\n')
		lineNo++
		if len(raw) > 0 {
			complete := raw[len(raw)-1] == '\n'
			payload := raw
			if complete {
				payload = raw[:len(raw)-1]
			}
			pe, perr := verifyLine(payload)
			if !complete {
				if rerr == io.EOF && perr == nil {
					events = append(events, *pe)
					return events, false, nil
				}
				// Unterminated, unparseable final line = crash mid-append.
				return events, true, nil
			}
			if perr != nil {
				return nil, false, fmt.Errorf("event log line %d: %w", lineNo, perr)
			}
			events = append(events, *pe)
		}
		if rerr == io.EOF {
			return events, false, nil
		}
		if rerr != nil {
			return nil, false, rerr
		}
	}
}

// appendEvent durably appends one event (write + fsync).
func appendEvent(dir string, e *Event) error {
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	env := envelope{Payload: payload, Checksum: hashBytes(payload)}
	b, err := json.Marshal(&env)
	if err != nil {
		return err
	}
	path := eventsPath(dir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// truncateEvents drops the trailing record(s) after a detected truncated
// tail, making the log consistent again.
func truncateEvents(dir string, keep int) error {
	path := eventsPath(dir)
	all, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cut := 0
	seen := 0
	for i := 0; i < len(all); i++ {
		if all[i] == '\n' {
			seen++
			if seen == keep {
				cut = i + 1
				break
			}
		}
	}
	if keep == 0 {
		cut = 0
	}
	return os.WriteFile(path, all[:cut], 0o644)
}
