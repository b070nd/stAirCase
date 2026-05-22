package ipc

import "github.com/b070nd/staircase-core/src/internal/domain"

// IpcStateEmit is sent by Python to Go on node completion.
type IpcStateEmit struct {
	Type         string                 `json:"type"` // "state_emit"
	ActiveAgent  string                 `json:"active_agent"`
	State        map[string]interface{} `json:"state,omitempty"`
	InputTokens  int                    `json:"input_tokens,omitempty"`
	OutputTokens int                    `json:"output_tokens,omitempty"`
	Model        string                 `json:"model,omitempty"`
}

// IpcYieldRequest and IpcYieldResponse are type aliases for the canonical types
// in the domain package.  Callers may use either name interchangeably; the ipc
// package re-exports them so existing code does not need to change its imports.
type (
	IpcYieldRequest  = domain.YieldRequest
	IpcYieldResponse = domain.YieldResponse
	ProposedEdit     = domain.ProposedEdit
)

// IpcSecretRequest is sent by Python to fetch an encrypted secret over the UDS.
// Secrets are never passed through environment variables (/proc leak prevention).
type IpcSecretRequest struct {
	Type      string `json:"type"` // "secret_request"
	KeyName   string `json:"key_name"`
	ProjectID *int64 `json:"project_id,omitempty"`
}

// IpcSecretResponse carries the decrypted secret plaintext back to Python.
// The field is named PlaintextValue to make it unambiguous that the Go server
// decrypts the secret before delivery — Python receives ready-to-use plaintext,
// never the AES ciphertext.
type IpcSecretResponse struct {
	Type           string `json:"type"`                      // "secret_response"
	PlaintextValue string `json:"plaintext_value,omitempty"` // decrypted; was "encrypted_value" before I-3 fix
	Error          string `json:"error,omitempty"`
}
