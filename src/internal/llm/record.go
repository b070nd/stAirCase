package llm

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// exchange is one line of a recording.
type exchange struct {
	Key      string   `json:"key"` // sha256 of the request
	Request  Request  `json:"request"`
	Response Response `json:"response"`
}

func requestKey(r Request) string {
	b, _ := json.Marshal(r)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Recorder passes calls on to a model and appends every exchange to a
// JSON-lines file (mode 0600: requests carry the prompts and the code read).
type Recorder struct {
	model Model
	mu    sync.Mutex
	f     *os.File
}

// NewRecorder records model's exchanges to path, replacing any old recording.
func NewRecorder(model Model, path string) (*Recorder, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open LLM recording: %w", err)
	}
	return &Recorder{model: model, f: f}, nil
}

// Chat implements Model.
func (r *Recorder) Chat(ctx context.Context, req Request) (Response, error) {
	resp, err := r.model.Chat(ctx, req)
	if err != nil {
		return resp, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := json.NewEncoder(r.f).Encode(exchange{Key: requestKey(req), Request: req, Response: resp}); err != nil {
		return resp, fmt.Errorf("write LLM recording: %w", err)
	}
	return resp, nil
}

// Close closes the recording.
func (r *Recorder) Close() error { return r.f.Close() }

// Replayer answers from a recording, offline: each request gets the response
// recorded for the identical request - in recorded order when one repeats -
// so concurrent agents replay deterministically. An unrecorded request is an
// error: a changed prompt, topology or file content fails loudly.
type Replayer struct {
	mu    sync.Mutex
	byKey map[string][]Response
}

// LoadReplay reads a recording made by Recorder.
func LoadReplay(path string) (*Replayer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open LLM recording: %w", err)
	}
	defer func() { _ = f.Close() }()
	r := &Replayer{byKey: map[string][]Response{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for n := 1; sc.Scan(); n++ {
		var ex exchange
		if err := json.Unmarshal(sc.Bytes(), &ex); err != nil {
			return nil, fmt.Errorf("LLM recording line %d: %w", n, err)
		}
		r.byKey[ex.Key] = append(r.byKey[ex.Key], ex.Response)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read LLM recording: %w", err)
	}
	return r, nil
}

// Chat implements Model.
func (r *Replayer) Chat(_ context.Context, req Request) (Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := requestKey(req)
	queue := r.byKey[k]
	if len(queue) == 0 {
		return Response{}, fmt.Errorf("replay: no recorded response for this %s request (key %s) - the prompts, tools or files differ from the recording", req.Model, k[:12])
	}
	r.byKey[k] = queue[1:]
	return queue[0], nil
}
