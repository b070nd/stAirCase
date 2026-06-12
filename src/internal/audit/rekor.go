// Rekor transparency-log anchoring (B3 — external audit anchoring).
//
// A signed checkpoint record proves integrity, but a workspace-local file can
// still be deleted or silently regenerated together with its signature by
// anyone holding the workspace key. Anchoring the record in a public Rekor
// transparency log adds an external, append-only witness: once anchored, the
// record's existence at a point in time can be proven to a third party.
//
// Entry type: `rekord` (artifact inline) rather than `hashedrekord`, because
// plain Ed25519 signs the full message — hashedrekord verifies over a digest,
// which requires ed25519ph. Checkpoint records are small NDJSON lines, so
// inlining the artifact is cheap and keeps verification sound server-side.
//
// Scope (documented limitation): VerifyAnchor confirms the log entry exists
// and its inlined artifact content matches the local record byte-for-byte.
// Full Merkle inclusion-proof / signed-tree-head verification is future work.
package audit

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"time"
)

// DefaultRekorURL is the public Sigstore Rekor instance.
const DefaultRekorURL = "https://rekor.sigstore.dev"

// rekorHTTP is the client used for all Rekor calls. Anchoring is an
// enterprise-evidence step, not a hot path — a generous timeout is fine.
var rekorHTTP = &http.Client{Timeout: 30 * time.Second}

// Anchor records where one checkpoint record landed in a Rekor log.
// Stored as NDJSON in the `<checkpoint>.anchor` sidecar, one per record.
type Anchor struct {
	RecordSHA256   string `json:"record_sha256"` // hex sha256 of the checkpoint NDJSON line
	UUID           string `json:"uuid"`
	LogIndex       int64  `json:"log_index"`
	LogID          string `json:"log_id"`
	IntegratedTime int64  `json:"integrated_time"` // unix seconds, per Rekor
	RekorURL       string `json:"rekor_url"`
}

// proposedEntry is the Rekor `rekord` v0.0.1 creation payload.
type proposedEntry struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Spec       rekordSpec `json:"spec"`
}

type rekordSpec struct {
	Data      rekordData `json:"data"`
	Signature rekordSig  `json:"signature"`
}

type rekordData struct {
	Content string `json:"content"` // base64 artifact
}

type rekordSig struct {
	Format    string       `json:"format"` // "x509" — PKIX public key, raw sig
	Content   string       `json:"content"`
	PublicKey rekordPubKey `json:"publicKey"`
}

type rekordPubKey struct {
	Content string `json:"content"` // base64 of PEM-encoded PKIX key
}

// logEntryBody is the decoded base64 `body` of a fetched Rekor entry —
// the same shape as proposedEntry; only the artifact content is compared.
type logEntryBody struct {
	Spec struct {
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	} `json:"spec"`
}

// AnchorRecord signs record with the workspace Ed25519 key and submits it to
// the Rekor log at rekorURL as a `rekord` entry. recordSHA256 must be the hex
// sha256 of record (computed by the caller, which already has it for the
// sidecar). Returns the anchor metadata to persist.
func AnchorRecord(rekorURL string, record []byte, recordSHA256 string, priv ed25519.PrivateKey) (Anchor, error) {
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return Anchor{}, fmt.Errorf("rekor anchor: signing key is not ed25519")
	}
	pkix, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return Anchor{}, fmt.Errorf("rekor anchor: marshal public key: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pkix})

	entry := proposedEntry{
		APIVersion: "0.0.1",
		Kind:       "rekord",
		Spec: rekordSpec{
			Data: rekordData{Content: base64.StdEncoding.EncodeToString(record)},
			Signature: rekordSig{
				Format:    "x509",
				Content:   base64.StdEncoding.EncodeToString(ed25519.Sign(priv, record)),
				PublicKey: rekordPubKey{Content: base64.StdEncoding.EncodeToString(pubPEM)},
			},
		},
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return Anchor{}, fmt.Errorf("rekor anchor: marshal entry: %w", err)
	}

	resp, err := rekorHTTP.Post(rekorURL+"/api/v1/log/entries", "application/json", bytes.NewReader(payload))
	if err != nil {
		return Anchor{}, fmt.Errorf("rekor anchor: submit: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		var msg bytes.Buffer
		_, _ = msg.ReadFrom(resp.Body)
		return Anchor{}, fmt.Errorf("rekor anchor: log returned %d: %s", resp.StatusCode, bytes.TrimSpace(msg.Bytes()))
	}

	// Response: {"<uuid>": {"logIndex":N,"logID":"…","integratedTime":N,…}}
	var created map[string]struct {
		LogIndex       int64  `json:"logIndex"`
		LogID          string `json:"logID"`
		IntegratedTime int64  `json:"integratedTime"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return Anchor{}, fmt.Errorf("rekor anchor: parse response: %w", err)
	}
	for uuid, meta := range created {
		return Anchor{
			RecordSHA256:   recordSHA256,
			UUID:           uuid,
			LogIndex:       meta.LogIndex,
			LogID:          meta.LogID,
			IntegratedTime: meta.IntegratedTime,
			RekorURL:       rekorURL,
		}, nil
	}
	return Anchor{}, fmt.Errorf("rekor anchor: empty response body")
}

// AppendAnchor appends one anchor record to the NDJSON sidecar at path,
// with the same append-only guarantees as AppendCheckpoint.
func AppendAnchor(path string, a Anchor) error {
	data, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("marshal anchor: %w", err)
	}
	return AppendCheckpoint(path, data) // same O_APPEND NDJSON semantics
}

// LoadAnchors reads all anchor records from the NDJSON sidecar at path.
// A missing file returns (nil, nil) — absence of anchors is not an error.
func LoadAnchors(path string) ([]Anchor, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open anchors: %w", err)
	}
	defer func() { _ = f.Close() }()

	var out []Anchor
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var a Anchor
		if err := json.Unmarshal(line, &a); err != nil {
			return nil, fmt.Errorf("parse anchor line: %w", err)
		}
		out = append(out, a)
	}
	return out, sc.Err()
}

// VerifyAnchor confirms that a checkpoint record (identified by recordSHA256,
// with content record) has a matching anchor in anchors, fetches the entry
// from its Rekor log, and verifies the log's inlined artifact equals the local
// record byte-for-byte. Returns the matched anchor on success.
func VerifyAnchor(record []byte, recordSHA256 string, anchors []Anchor) (Anchor, error) {
	var match *Anchor
	for i := range anchors {
		if anchors[i].RecordSHA256 == recordSHA256 {
			match = &anchors[i]
			break
		}
	}
	if match == nil {
		return Anchor{}, fmt.Errorf("no anchor found for record sha256=%s", recordSHA256)
	}

	resp, err := rekorHTTP.Get(match.RekorURL + "/api/v1/log/entries/" + match.UUID)
	if err != nil {
		return Anchor{}, fmt.Errorf("rekor fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Anchor{}, fmt.Errorf("rekor fetch: log returned %d for uuid %s", resp.StatusCode, match.UUID)
	}

	// Response: {"<uuid>": {"body": "<base64 rekord entry>", ...}}
	var fetched map[string]struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fetched); err != nil {
		return Anchor{}, fmt.Errorf("rekor fetch: parse response: %w", err)
	}
	entry, ok := fetched[match.UUID]
	if !ok {
		return Anchor{}, fmt.Errorf("rekor fetch: uuid %s missing from response", match.UUID)
	}

	bodyRaw, err := base64.StdEncoding.DecodeString(entry.Body)
	if err != nil {
		return Anchor{}, fmt.Errorf("rekor fetch: decode body: %w", err)
	}
	var body logEntryBody
	if err := json.Unmarshal(bodyRaw, &body); err != nil {
		return Anchor{}, fmt.Errorf("rekor fetch: parse body: %w", err)
	}
	logged, err := base64.StdEncoding.DecodeString(body.Spec.Data.Content)
	if err != nil {
		return Anchor{}, fmt.Errorf("rekor fetch: decode artifact: %w", err)
	}
	if !bytes.Equal(logged, record) {
		return Anchor{}, fmt.Errorf("rekor anchor MISMATCH: logged artifact differs from local record (uuid %s)", match.UUID)
	}
	return *match, nil
}
