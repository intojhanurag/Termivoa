// Code generated from JSON Schema using quicktype. DO NOT EDIT.
// To parse and unparse this JSON data, add this code to your project and do:
//
//    envelope, err := UnmarshalEnvelope(bytes)
//    bytes, err = envelope.Marshal()

package protocol

import "encoding/json"

func UnmarshalEnvelope(data []byte) (Envelope, error) {
	var r Envelope
	err := json.Unmarshal(data, &r)
	return r, err
}

func (r *Envelope) Marshal() ([]byte, error) {
	return json.Marshal(r)
}

// Bootstrap envelope only. P2 defines the complete v1 message union before clients depend
// on it.
type Envelope struct {
	Generation *string                `json:"generation,omitempty"`
	Payload    map[string]interface{} `json:"payload"`
	RequestID  string                 `json:"requestId"`
	SessionID  *string                `json:"sessionId,omitempty"`
	Type       string                 `json:"type"`
	Version    float64                `json:"version"`
}
