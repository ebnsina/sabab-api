// Package ingest defines what travels on the queue: the gateway's output and
// the processor's input.
//
// A Job is one envelope item plus the context only the gateway can establish —
// who sent it, when we received it, and which project it belongs to. The
// payload stays raw: parsing it is the processor's job, and doing it in the
// gateway would put JSON decoding of attacker-controlled data on the hot path
// for no benefit.
package ingest

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ebnsina/sabab-api/internal/event"
)

// Job is one queued envelope item.
type Job struct {
	// ProjectID is resolved by the gateway from the ingest key, never read from
	// the payload. This is the field that stops a client writing into another
	// tenant's project by simply claiming to.
	ProjectID uint64 `json:"project_id"`

	Type    event.Kind      `json:"type"`
	Payload json.RawMessage `json:"payload"`

	// ReceivedAt is our clock. Client timestamps are subject to skew — a device
	// with a wrong clock would otherwise land events in the far future or the
	// distant past — so the processor corrects against this.
	ReceivedAt time.Time `json:"received_at"`
	// SentAt is the client's clock at flush time, from the envelope header. The
	// pair (ReceivedAt, SentAt) is what makes skew correction possible.
	SentAt time.Time `json:"sent_at,omitzero"`

	SDK event.SDK `json:"sdk"`

	// ClientIP is the socket address the gateway saw. It resolves the SDK's
	// "{{auto}}" IP placeholder, and it is subject to the scrubber before it is
	// ever persisted.
	ClientIP string `json:"client_ip,omitempty"`
}

// Encode serializes a job for the queue.
func Encode(job Job) ([]byte, error) {
	body, err := json.Marshal(job)
	if err != nil {
		return nil, fmt.Errorf("encode job: %w", err)
	}
	return body, nil
}

// Decode parses a job off the queue.
func Decode(body []byte) (Job, error) {
	var job Job
	if err := json.Unmarshal(body, &job); err != nil {
		return Job{}, fmt.Errorf("decode job: %w", err)
	}
	if job.ProjectID == 0 {
		return Job{}, fmt.Errorf("decode job: project_id is missing")
	}
	if !job.Type.Valid() {
		return Job{}, fmt.Errorf("decode job: unknown type %q", job.Type)
	}
	return job, nil
}
