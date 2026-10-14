// Package proto defines the messages exchanged between the server, the tech
// console in the browser, and the agent running on the end user's computer.
//
// Signaling messages travel over WebSockets through the server. Once the
// WebRTC connection is up, control messages travel peer-to-peer over data
// channels. The TypeScript mirror of these types lives in web/src/lib/proto.ts
// and must be kept in sync.
package proto

import "encoding/json"

// Version is bumped whenever the wire protocol changes incompatibly.
const Version = 1

// Envelope wraps every signaling message.
type Envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Signaling message types.
const (
	// Agent -> server, first message on the agent socket.
	TypeJoin = "join"
	// Server -> agent and server -> tech after a successful join.
	TypeHello = "hello"
	// Agent -> server -> tech.
	TypeAgentInfo = "agent_info"
	// Agent -> server -> tech: the user's answer to the consent prompt.
	TypeConsent = "consent"
	// Server -> tech: session state changed.
	TypeStatus = "status"
	// WebRTC negotiation, relayed verbatim between tech and agent.
	TypeOffer  = "offer"
	TypeAnswer = "answer"
	TypeICE    = "ice"
	// Either side: end the session.
	TypeEnd = "end"
	// Tech -> agent: ask the user to relaunch the agent as administrator.
	TypeElevateRequest = "elevate_request"
	// Either side -> server: record an audit event.
	TypeEvent = "event"
	// Server -> client: something went wrong.
	TypeError = "error"
)

// Join is sent by the agent to attach to a support session.
type Join struct {
	ProtocolVersion int    `json:"protocol_version"`
	Code            string `json:"code,omitempty"`
	// ResumeToken lets the same agent reconnect (for example after relaunching
	// elevated) without reusing the one-time code.
	ResumeToken string `json:"resume_token,omitempty"`
}

// ICEServer mirrors the browser RTCIceServer dictionary.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// Hello is sent by the server to both peers once a session is joined.
type Hello struct {
	SessionID   string      `json:"session_id"`
	OrgName     string      `json:"org_name"`
	TechName    string      `json:"tech_name"`
	TechEmail   string      `json:"tech_email,omitempty"`
	ICEServers  []ICEServer `json:"ice_servers"`
	ResumeToken string      `json:"resume_token,omitempty"`
	// Resumed is true when the agent reattached with a resume token and the
	// user has already consented in this session.
	Resumed bool `json:"resumed,omitempty"`
}

// AgentInfo describes the end user's computer.
type AgentInfo struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Username string `json:"username"`
	Version  string `json:"version"`
	Elevated bool   `json:"elevated"`
}

// Consent carries the user's decision.
type Consent struct {
	Allowed bool `json:"allowed"`
}

// Session states.
const (
	StatePending         = "pending"          // code issued, waiting for agent
	StateAwaitingConsent = "awaiting_consent" // agent connected, user deciding
	StateActive          = "active"           // user allowed the connection
	StateEnded           = "ended"            // finished (see end reason)
	StateExpired         = "expired"          // code never used
)

// Status is sent to the tech whenever the session changes.
type Status struct {
	State          string     `json:"state"`
	AgentConnected bool       `json:"agent_connected"`
	Agent          *AgentInfo `json:"agent,omitempty"`
	EndReason      string     `json:"end_reason,omitempty"`
}

// SDP carries an offer or answer.
type SDP struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

// ICECandidate mirrors RTCIceCandidateInit.
type ICECandidate struct {
	Candidate        string  `json:"candidate"`
	SDPMid           *string `json:"sdpMid,omitempty"`
	SDPMLineIndex    *uint16 `json:"sdpMLineIndex,omitempty"`
	UsernameFragment *string `json:"usernameFragment,omitempty"`
}

// End terminates the session.
type End struct {
	Reason string `json:"reason"`
}

// Event is an auditable action reported by a peer.
type Event struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

// Error reports a problem to a client.
type Error struct {
	Message string `json:"message"`
}

// Marshal builds an envelope.
func Marshal(typ string, v any) ([]byte, error) {
	env := Envelope{Type: typ}
	if v != nil {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		env.Data = b
	}
	return json.Marshal(env)
}

// Data channel labels. The tech browser creates these when it makes the offer.
const (
	ChannelVideo   = "video"
	ChannelInput   = "input"
	ChannelControl = "control"
	ChannelFiles   = "files"
)
