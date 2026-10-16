package api

import "time"

// CredentialHelperEnv is the environment variable that points a seat at its
// credential helper. The value is the helper's path, never a token: the
// helper asks the broker for one and prints it to the harness that called it.
const CredentialHelperEnv = "MARVEL_CREDENTIAL_HELPER"

// BrokerTokenRequest is the broker.token RPC's wire shape. The seat
// authenticates with the heartbeat token marvel put in its environment.
type BrokerTokenRequest struct {
	SessionKey   string `json:"session_key"`
	SessionToken string `json:"session_token,omitempty"`
}

// BrokerTokenResponse carries one short-lived token. Value is bearer
// authority until Expires, so it travels from the daemon to the helper and
// nowhere else.
type BrokerTokenResponse struct {
	ID      string    `json:"id"`
	Value   string    `json:"value"`
	Expires time.Time `json:"expires"`
}
