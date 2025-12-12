package imapclt

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

type Message struct {
	UID      uint32
	Message  io.Reader
	Envelope Envelope
}

type Envelope struct {
	Date    time.Time
	Subject string
	From    []string
	// Recipients are the To, Cc and Bcc addresses
	Recipients []string
	MessageID  string
}

var errMalformedEnvelope = errors.New("malformed IMAP ENVELOPE")

func isMalformedEnvelopeErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errMalformedEnvelope) {
		return true
	}

	// Known go-imapwire parse errors surface either during Collect() or Close():
	// "in response-data: in envelope: imapwire: expected ')', got \"7\""
	s := err.Error()
	return strings.Contains(s, "in envelope") ||
		strings.Contains(s, "imapwire: expected ')',")
}

// Messages returns an iterator over the messages in mailbox.
// When an error happens a nil message and an error is passed via the yield
// function.
func (c *Client) Messages(mailbox string) iter.Seq2[*Message, error] {
	return func(yield func(*Message, error) bool) {
		logger := c.logger.With(lkMailbox, mailbox)
		mbox, err := c.clt.Select(mailbox, &imap.SelectOptions{}).Wait()
		if err != nil {
			yield(nil, fmt.Errorf("selecting mailbox failed: %w", err))
			return
		}

		if mbox.NumMessages == 0 {
			logger.Debug("mailbox is empty", "event", "imap.mailbox_empty")
			return
		}

		logger.Debug(
			"new messages found",
			"event",
			"imap.new_messages",
			"count", mbox.NumMessages,
		)

		n := imap.SeqSet{}
		n.AddRange(1, 0)

		fetchCmd := c.clt.Fetch(n, &imap.FetchOptions{
			Envelope:    true,
			UID:         true,
			BodySection: []*imap.FetchItemBodySection{{Peek: true}},
		})

		var canceled bool
		for {
			msg, err := c.fetchNext(fetchCmd)
			if err != nil {
				// Critical: malformed ENVELOPEs must not crash the service.
				if isMalformedEnvelopeErr(err) {
					logger.Warn("skipping message due to malformed ENVELOPE", "error", err)
					continue
				}

				canceled = !yield(nil, err)
				break
			}

			if msg == nil {
				break
			}

			canceled = !yield(msg, nil)
			if canceled {
				break
			}
		}

		err = fetchCmd.Close()
		if err != nil {
			// go-imapwire sometimes reports ENVELOPE parse errors here; ignore them.
			if isMalformedEnvelopeErr(err) {
				logger.Warn("releasing fetch command failed (malformed ENVELOPE; ignored)", "error", err)
				if !canceled {
					yield(nil, fmt.Errorf("releasing fetch command failed (malformed ENVELOPE): %w", err))
				}
				return
			}

			if !canceled {
				yield(nil, fmt.Errorf("releasing fetch command failed: %w", err))
				return
			}

			logger.Warn("releasing fetch command failed", "error", err)
		}
	}
}

// fetchNext calls Next() and returns the message as [Message].
// When there is no next message nil,nil is returned.
func (c *Client) fetchNext(fetchCmd *imapclient.FetchCommand) (*Message, error) {
	msgData := fetchCmd.Next()
	if msgData == nil {
		return nil, nil
	}

	msg, err := msgData.Collect()
	if err != nil {
		// May include ENVELOPE parse errors; caller decides whether to skip.
		return nil, fmt.Errorf("collecting message failed: %w", err)
	}

	if msg.UID == 0 {
		return nil, fmt.Errorf("message uid is 0")
	}
	if msg.Envelope == nil {
		// Return a sentinel so the caller can skip instead of terminating.
		return nil, fmt.Errorf("%w: uid=%d", errMalformedEnvelope, msg.UID)
	}

	logger := c.logger.With(
		"mail.subject", msg.Envelope.Subject,
		"mail.uid", msg.UID,
	)
	logger.Debug("fetched message")

	body := msg.FindBodySection(&imap.FetchItemBodySection{})
	if body == nil {
		return nil, errors.New("message is missing body section")
	}

	if len(body) == 0 {
		return nil, errors.New("message data reader is empty")
	}

	return &Message{
		UID: uint32(msg.UID),
		// TODO: Can we stream the body instead of
		// storing it in memory?
		Message: bytes.NewReader(body),
		Envelope: Envelope{
			Date:    msg.Envelope.Date,
			Subject: msg.Envelope.Subject,
			From:    addressesToStrings(msg.Envelope.From),
			Recipients: slices.Concat(
				addressesToStrings(msg.Envelope.To),
				addressesToStrings(msg.Envelope.Cc),
				addressesToStrings(msg.Envelope.Cc),
			),
		},
	}, nil
}

func addressesToStrings(addrs []imap.Address) []string {
	result := make([]string, 0, len(addrs))

	for _, addr := range addrs {
		result = append(result, addr.Addr())
	}

	return result
}
