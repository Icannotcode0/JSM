// Package mailer is the seam between JSM and however mail actually leaves the
// machine.
//
// It exists before there is any real transport on purpose. Retrofitting an
// interface is cheap while there is one call site and expensive once there are
// several, and JSM has a specific reason to need the seam early: the product
// promises that nothing leaves your computer. Local installs must therefore be
// able to run with no transport at all, while a hosted deployment sends real
// mail — the same code path, a different implementation.
package mailer

import (
	"context"
	"fmt"
	"log"
)

// Message is one outbound email.
//
// Plain text only for now. An HTML alternative is an added field rather than a
// new method, so callers that don't care never learn about it.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Mailer delivers a Message.
//
// Send is synchronous and returns a delivery *attempt* error — the transport
// accepted it, or it didn't. It deliberately says nothing about whether the mail
// arrived, because no transport can tell you that at call time.
//
// Callers on an HTTP path should think about whether they want to block on it.
// A signup that waits on an SMTP round-trip is a signup that fails when the mail
// provider is slow. Sending in a goroutine is usually right, and that decision
// belongs to the caller, not to this interface.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// LogMailer writes messages to the standard logger instead of sending them.
//
// This is the local-first default, and it is a feature rather than a stub: with
// no transport configured JSM still has no outbound network access, and a
// developer can read the verification code straight out of the server log
// without registering for a mail provider.
type LogMailer struct{}

func NewLogMailer() *LogMailer { return &LogMailer{} }

func (LogMailer) Send(_ context.Context, msg Message) error {
	log.Printf("[mailer] (not sent — LogMailer)\n  to:      %s\n  subject: %s\n  body:\n%s",
		msg.To, msg.Subject, msg.Body)
	return nil
}

// DiscardMailer drops messages silently. Intended for tests that exercise a
// path which happens to send mail without being about the mail.
type DiscardMailer struct{}

func NewDiscardMailer() *DiscardMailer { return &DiscardMailer{} }

func (DiscardMailer) Send(_ context.Context, _ Message) error { return nil }

// RecordingMailer keeps what it was asked to send, so a test can assert on the
// recipient and body without a transport.
//
// Not safe for concurrent use; tests that send from multiple goroutines should
// guard it themselves.
type RecordingMailer struct {
	Sent []Message
	Err  error // when set, Send fails with it — for exercising failure paths
}

func NewRecordingMailer() *RecordingMailer { return &RecordingMailer{} }

func (m *RecordingMailer) Send(_ context.Context, msg Message) error {
	if m.Err != nil {
		return m.Err
	}
	m.Sent = append(m.Sent, msg)
	return nil
}

// Last returns the most recent message, and whether there was one.
func (m *RecordingMailer) Last() (Message, bool) {
	if len(m.Sent) == 0 {
		return Message{}, false
	}
	return m.Sent[len(m.Sent)-1], true
}

// ErrNoTransport is what a future SMTP implementation should return when it is
// constructed without the configuration it needs, rather than sending nothing
// and reporting success.
var ErrNoTransport = fmt.Errorf("mailer: no transport configured")
