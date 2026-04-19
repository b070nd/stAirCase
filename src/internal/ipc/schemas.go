
package ipc

// IpcStateEmit is sent by Python to Go on node completion.
type IpcStateEmit struct {
	Type         string                 `json:"type"` // "state_emit"
	ActiveAgent  string                 `json:"active_agent"`
	State        map[string]interface{} `json:"state"`
	InputTokens  int                    `json:"input_tokens,omitempty"`
	OutputTokens int                    `json:"output_tokens,omitempty"`
	Model        string                 `json:"model,omitempty"`
}

// IpcYieldRequest is sent by Python to request HITL approval.
type IpcYieldRequest struct {
	Type            string          `json:"type"` // "yield_request"
	AgentName       string          `json:"agent_name"`
	ActionType      string          `json:"action_type"` // "file_edit", "shell_exec"
	ProposedEdits   []ProposedEdit  `json:"proposed_edits,omitempty"`
	ReasoningTrace  string          `json:"reasoning_trace"`
	ConfidenceScore float64         `json:"confidence_score"`
	BatchID         string          `json:"batch_id,omitempty"`
}

type ProposedEdit struct {
	File         string `json:"file"`
	SearchBlock  string `json:"search_block"`
	ReplaceBlock string `json:"replace_block"`
}

// IpcYieldResponse is sent by Go to Python via stdin.
type IpcYieldResponse struct {
	Type     string `json:"type"` // "yield_response"
	Approved bool   `json:"approved"`
	Feedback string `json:"feedback,omitempty"`
}

// IpcSecretRequest is sent by Python to fetch an encrypted secret over the UDS.
// Secrets are never passed through environment variables (/proc leak prevention).
type IpcSecretRequest struct {
	Type      string `json:"type"` // "secret_request"
	KeyName   string `json:"key_name"`
	ProjectID *int64 `json:"project_id,omitempty"`
}

// IpcSecretResponse carries the encrypted secret value back to Python.
type IpcSecretResponse struct {
	Type           string `json:"type"` // "secret_response"
	EncryptedValue string `json:"encrypted_value,omitempty"`
	Error          string `json:"error,omitempty"`
}