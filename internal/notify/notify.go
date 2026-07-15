// Package notify delivers an alert to the channels a rule names.
//
// The governing rule here mirrors the SDK's: a notification path must never take
// down the thing it serves. One misconfigured Slack webhook must not block the
// email that would have paged the on-call engineer, and a hanging HTTP call must
// not stall the whole alerter. So every channel is dispatched independently,
// under its own timeout, and a failure is logged and moved past — never
// propagated.
package notify

import (
	"context"
	"fmt"
	"time"
)

// Notification is a rendered alert, ready to send. It is channel-agnostic; each
// Sender formats it for its own medium.
type Notification struct {
	// Reason is why this fired, in a few words: "New issue", "Regression",
	// "High frequency". It becomes the alert's headline.
	Reason string

	ProjectName string
	Title       string // the issue title
	Culprit     string
	Level       string
	Release     string
	Environment string

	// Count and Window describe a frequency alert ("1,240 events in 5m"). Zero
	// Count means this is not a frequency alert and the line is omitted.
	Count  uint64
	Window string

	// URL links straight to the issue in the dashboard. An alert you cannot act
	// on from your phone is only half an alert.
	URL string

	// FiredAt is when the alert was raised (our clock), passed in rather than
	// read from a global so the whole thing stays testable.
	FiredAt time.Time
}

// Sender delivers to one channel. Implementations must respect ctx and return
// promptly; Dispatch applies its own deadline regardless.
type Sender interface {
	// Kind is the channel type as it appears in a rule's config: "slack",
	// "discord", "webhook", "email".
	Kind() string
	// Send delivers n using the per-channel settings in cfg.
	Send(ctx context.Context, cfg ChannelConfig, n Notification) error
}

// ChannelConfig is one entry from a rule's `channels` JSON array. The fields a
// given channel needs differ, so it is a loose map validated by each Sender
// rather than a rigid struct — a Slack channel needs a webhook_url, an email
// channel needs a to-address.
type ChannelConfig struct {
	Type     string            `json:"type"`
	Settings map[string]string `json:"settings"`
}

// perChannelTimeout bounds a single delivery. Well under the alerter's
// evaluation interval, so a dead endpoint cannot back the whole loop up.
const perChannelTimeout = 10 * time.Second

// Dispatcher routes a notification to the registered senders.
type Dispatcher struct {
	senders map[string]Sender
}

// NewDispatcher builds a Dispatcher from the available senders.
func NewDispatcher(senders ...Sender) *Dispatcher {
	byKind := make(map[string]Sender, len(senders))
	for _, s := range senders {
		byKind[s.Kind()] = s
	}
	return &Dispatcher{senders: byKind}
}

// Result records the outcome for one channel, so the alerter can log exactly
// what reached whom and what did not.
type Result struct {
	Channel string
	Err     error
}

// Dispatch sends n to every channel, independently. It returns one Result per
// channel and never returns an error itself — a channel failing is data to
// record, not a reason to abort the others.
func (d *Dispatcher) Dispatch(ctx context.Context, channels []ChannelConfig, n Notification) []Result {
	results := make([]Result, 0, len(channels))

	for _, ch := range channels {
		sender, ok := d.senders[ch.Type]
		if !ok {
			results = append(results, Result{
				Channel: ch.Type,
				Err:     fmt.Errorf("no sender configured for channel %q", ch.Type),
			})
			continue
		}

		// Each channel gets its own deadline. A slow Slack must not eat into the
		// budget of the email after it.
		chCtx, cancel := context.WithTimeout(ctx, perChannelTimeout)
		err := sender.Send(chCtx, ch, n)
		cancel()

		results = append(results, Result{Channel: ch.Type, Err: err})
	}

	return results
}
