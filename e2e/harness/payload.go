//go:build e2e

package harness

import "encoding/json"

// payloadBuilder collects the server-sent event payloads of one scripted
// response, remembering the first encoding failure so a codec can build a
// whole response without checking every step.
type payloadBuilder struct {
	payloads []string
	err      error
}

// add encodes one payload and appends it to the response.
func (b *payloadBuilder) add(payload any) {
	if b.err != nil {
		return
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		b.err = err
		return
	}
	b.payloads = append(b.payloads, string(encoded))
}

// addRaw appends a payload that is already encoded, used for the sentinels
// that close a stream.
func (b *payloadBuilder) addRaw(payload string) {
	if b.err == nil {
		b.payloads = append(b.payloads, payload)
	}
}

// done returns the collected payloads and the first failure, if any.
func (b *payloadBuilder) done() ([]string, error) {
	return b.payloads, b.err
}
