package privacy

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"runtime"
	"sync"
)

const (
	presidioSocketPath    = "/tmp/prism-presidio.sock"
	presidioWindowsAddr   = "127.0.0.1:5001"
	presidioMaxMsgSize    = 10 * 1024 * 1024 // 10 MB
)

type presidioDetector struct {
	mu        sync.Mutex
	conn      net.Conn
	semaphore chan struct{}
	tlsConfig *tls.Config // Windows only
	addr      string      // address (socket path or host:port)
}

// PresidioOption configures the Presidio detector.
type PresidioOption func(*presidioDetector)

// WithPresidioTLS sets TLS configuration (used on Windows).
func WithPresidioTLS(cfg *tls.Config) PresidioOption {
	return func(pd *presidioDetector) { pd.tlsConfig = cfg }
}

// WithPresidioAddr overrides the default address.
func WithPresidioAddr(addr string) PresidioOption {
	return func(pd *presidioDetector) { pd.addr = addr }
}

// NewPresidioDetector creates a Tier 3 detector communicating with the Presidio
// Python sidecar. On Unix, uses a domain socket (chmod 600). On Windows, uses
// loopback + TLS.
// maxConcurrent limits concurrent requests to the sidecar (= spaCy worker count).
func NewPresidioDetector(maxConcurrent int, opts ...PresidioOption) (Detector, error) {
	pd := &presidioDetector{
		semaphore: make(chan struct{}, maxConcurrent),
	}
	for _, o := range opts {
		o(pd)
	}

	conn, err := pd.dial()
	if err != nil {
		return nil, fmt.Errorf("presidio: connect: %w", err)
	}
	pd.conn = conn
	return pd, nil
}

func (pd *presidioDetector) dial() (net.Conn, error) {
	// Explicit TLS config — always use TLS.
	if pd.tlsConfig != nil {
		addr := pd.addr
		if addr == "" {
			addr = presidioWindowsAddr
		}
		return tls.Dial("tcp", addr, pd.tlsConfig)
	}

	// Explicit address without TLS — use plain connection.
	// Allows testing with a mock server on any platform.
	if pd.addr != "" {
		if runtime.GOOS == "windows" {
			return net.Dial("tcp", pd.addr)
		}
		return net.Dial("unix", pd.addr)
	}

	// Default: Unix socket on non-Windows, TLS on Windows.
	if runtime.GOOS == "windows" {
		return tls.Dial("tcp", presidioWindowsAddr, &tls.Config{MinVersion: tls.VersionTLS13})
	}
	return net.Dial("unix", presidioSocketPath)
}

// presidioRequest is sent to the Presidio sidecar.
type presidioRequest struct {
	Text     string   `json:"text"`
	Language string   `json:"language"`
	Entities []string `json:"entities,omitempty"`
}

// presidioEntity is returned by the Presidio sidecar.
type presidioEntity struct {
	EntityType string  `json:"entity_type"`
	Text       string  `json:"text"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Score      float64 `json:"score"`
}

func (pd *presidioDetector) Detect(ctx context.Context, text string, profile Profile) ([]Entity, error) {
	if profile == ProfileOff {
		return nil, nil
	}

	// Acquire semaphore — limit concurrent load on the sidecar.
	select {
	case pd.semaphore <- struct{}{}:
		defer func() { <-pd.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	req := presidioRequest{
		Text:     text,
		Language: detectLanguage(text),
	}
	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("presidio: marshal: %w", err)
	}

	pd.mu.Lock()
	respData, err := pd.roundTrip(reqData)
	pd.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("presidio: roundtrip: %w", err)
	}

	var presEntities []presidioEntity
	if err := json.Unmarshal(respData, &presEntities); err != nil {
		return nil, fmt.Errorf("presidio: decode response: %w", err)
	}

	entities := make([]Entity, 0, len(presEntities))
	for _, pe := range presEntities {
		entities = append(entities, Entity{
			Type:  pe.EntityType,
			Value: pe.Text,
			Score: pe.Score,
			Start: pe.Start,
			End:   pe.End,
		})
	}
	return entities, nil
}

// roundTrip sends a length-prefixed JSON message and reads the response.
// Protocol: [4-byte big-endian length][JSON payload]
// Must be called while holding pd.mu.
func (pd *presidioDetector) roundTrip(data []byte) ([]byte, error) {
	// Write length prefix + data.
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if _, err := pd.conn.Write(header); err != nil {
		return nil, fmt.Errorf("write header: %w", err)
	}
	if _, err := pd.conn.Write(data); err != nil {
		return nil, fmt.Errorf("write body: %w", err)
	}

	// Read response length.
	if _, err := io.ReadFull(pd.conn, header); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	respLen := binary.BigEndian.Uint32(header)
	if respLen > presidioMaxMsgSize {
		return nil, fmt.Errorf("response too large: %d bytes", respLen)
	}

	// Read response body.
	respData := make([]byte, respLen)
	if _, err := io.ReadFull(pd.conn, respData); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return respData, nil
}

func (pd *presidioDetector) HealthCheck(ctx context.Context) error {
	pd.mu.Lock()
	defer pd.mu.Unlock()

	// Send a minimal detection request as a health probe.
	req, _ := json.Marshal(presidioRequest{Text: "health", Language: "en"})
	_, err := pd.roundTrip(req)
	if err != nil {
		return fmt.Errorf("presidio: health check: %w", err)
	}
	return nil
}

// detectLanguage returns a BCP-47 language tag heuristic.
// Checks for Cyrillic characters to distinguish Russian from English.
func detectLanguage(text string) string {
	for _, r := range text {
		if r >= 0x0400 && r <= 0x04FF { // Cyrillic range
			return "ru"
		}
	}
	return "en"
}
