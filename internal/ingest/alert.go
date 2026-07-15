package ingest

import (
	"encoding/json"
	"fmt"
	"time"
)

// AlertKind is what the processor observed that might be worth alerting on.
type AlertKind string

const (
	// AlertNewIssue: a group we had never seen before.
	AlertNewIssue AlertKind = "new_issue"
	// AlertRegression: a resolved issue that started happening again — the most
	// valuable alert an error tracker sends, because it means a fix did not hold.
	AlertRegression AlertKind = "regression"
)

// AlertSignal is what the processor publishes when it detects a new issue or a
// regression, and what the alerter consumes.
//
// It carries everything an alert needs to render WITHOUT the alerter having to
// query back for it: matching a rule and firing a notification must not each
// cost a database round trip per event, or the alerter becomes the bottleneck
// behind the processor.
type AlertSignal struct {
	Kind      AlertKind `json:"kind"`
	ProjectID uint64    `json:"project_id"`
	IssueID   uint64    `json:"issue_id"`
	GroupHash string    `json:"group_hash"`

	Title       string    `json:"title"`
	Culprit     string    `json:"culprit"`
	Level       string    `json:"level"`
	Release     string    `json:"release,omitempty"`
	Environment string    `json:"environment,omitempty"`
	At          time.Time `json:"at"`
}

// EncodeAlert serialises a signal for the queue.
func EncodeAlert(sig AlertSignal) ([]byte, error) {
	body, err := json.Marshal(sig)
	if err != nil {
		return nil, fmt.Errorf("encode alert signal: %w", err)
	}
	return body, nil
}

// DecodeAlert parses a signal off the queue.
func DecodeAlert(body []byte) (AlertSignal, error) {
	var sig AlertSignal
	if err := json.Unmarshal(body, &sig); err != nil {
		return AlertSignal{}, fmt.Errorf("decode alert signal: %w", err)
	}
	if sig.ProjectID == 0 || sig.IssueID == 0 {
		return AlertSignal{}, fmt.Errorf("decode alert signal: missing project or issue id")
	}
	return sig, nil
}
