package privacy_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/helldriver666/prism/internal/privacy"
)

// mockPresidioServer creates a mock Presidio sidecar on a temporary Unix socket
// (or named pipe on Windows). Returns the listener address and a cleanup function.
func mockPresidioServer(t *testing.T, handler func([]byte) []byte) (string, func()) {
	t.Helper()

	var listener net.Listener
	var addr string
	var err error

	if runtime.GOOS == "windows" {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr = listener.Addr().String()
	} else {
		dir := t.TempDir()
		sockPath := dir + "/presidio.sock"
		listener, err = net.Listen("unix", sockPath)
		if err != nil {
			t.Fatal(err)
		}
		addr = sockPath
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				for {
					// Read length prefix.
					header := make([]byte, 4)
					if _, err := c.Read(header); err != nil {
						return
					}
					msgLen := binary.BigEndian.Uint32(header)
					data := make([]byte, msgLen)
					if _, err := c.Read(data); err != nil {
						return
					}

					resp := handler(data)

					// Write response.
					binary.BigEndian.PutUint32(header, uint32(len(resp)))
					c.Write(header)
					c.Write(resp)
				}
			}(conn)
		}
	}()

	return addr, func() { listener.Close() }
}

func TestPresidioDetector_Detect_Success(t *testing.T) {
	addr, cleanup := mockPresidioServer(t, func(data []byte) []byte {
		resp := []struct {
			EntityType string  `json:"entity_type"`
			Text       string  `json:"text"`
			Start      int     `json:"start"`
			End        int     `json:"end"`
			Score      float64 `json:"score"`
		}{
			{EntityType: "PERSON", Text: "John Doe", Start: 0, End: 8, Score: 0.95},
		}
		b, _ := json.Marshal(resp)
		return b
	})
	defer cleanup()

	det, err := privacy.NewPresidioDetector(4, privacy.WithPresidioAddr(addr))
	if err != nil {
		t.Fatal(err)
	}

	entities, err := det.Detect(context.Background(), "John Doe sent an email", privacy.ProfileModerate)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(entities))
	}
	if entities[0].Type != "PERSON" || entities[0].Value != "John Doe" {
		t.Errorf("unexpected entity: %+v", entities[0])
	}
}

func TestPresidioDetector_ProfileOff(t *testing.T) {
	addr, cleanup := mockPresidioServer(t, func(data []byte) []byte {
		return []byte("[]")
	})
	defer cleanup()

	det, err := privacy.NewPresidioDetector(4, privacy.WithPresidioAddr(addr))
	if err != nil {
		t.Fatal(err)
	}

	entities, err := det.Detect(context.Background(), "test", privacy.ProfileOff)
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 0 {
		t.Error("ProfileOff should return nil")
	}
}

func TestPresidioDetector_Semaphore_RateLimiting(t *testing.T) {
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32

	addr, cleanup := mockPresidioServer(t, func(data []byte) []byte {
		c := concurrent.Add(1)
		// Track max concurrency.
		for {
			old := maxConcurrent.Load()
			if c <= old || maxConcurrent.CompareAndSwap(old, c) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		concurrent.Add(-1)
		return []byte("[]")
	})
	defer cleanup()

	maxWorkers := 2
	det, err := privacy.NewPresidioDetector(maxWorkers, privacy.WithPresidioAddr(addr))
	if err != nil {
		t.Fatal(err)
	}

	// Fire more requests than the semaphore allows.
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			det.Detect(context.Background(), "test", privacy.ProfileModerate)
		}()
	}
	wg.Wait()

	// Max concurrent should not exceed the semaphore limit.
	if got := maxConcurrent.Load(); got > int32(maxWorkers) {
		t.Errorf("max concurrent %d exceeded semaphore limit %d", got, maxWorkers)
	}
}

func TestPresidioDetector_ContextCancellation(t *testing.T) {
	addr, cleanup := mockPresidioServer(t, func(data []byte) []byte {
		time.Sleep(5 * time.Second) // Slow server.
		return []byte("[]")
	})
	defer cleanup()

	// Create with semaphore of 1 and block it.
	det, err := privacy.NewPresidioDetector(1, privacy.WithPresidioAddr(addr))
	if err != nil {
		t.Fatal(err)
	}

	// First request will take the semaphore.
	go det.Detect(context.Background(), "test", privacy.ProfileModerate)
	time.Sleep(10 * time.Millisecond) // Let first request grab the semaphore.

	// Second request should be cancelled.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = det.Detect(ctx, "test2", privacy.ProfileModerate)
	if err == nil {
		t.Error("expected error from context cancellation")
	}
}

func TestPresidioDetector_HealthCheck(t *testing.T) {
	addr, cleanup := mockPresidioServer(t, func(data []byte) []byte {
		return []byte("[]")
	})
	defer cleanup()

	det, err := privacy.NewPresidioDetector(4, privacy.WithPresidioAddr(addr))
	if err != nil {
		t.Fatal(err)
	}

	if err := det.HealthCheck(context.Background()); err != nil {
		t.Fatal("expected healthy:", err)
	}
}

func TestPresidioDetector_ConnectionRefused(t *testing.T) {
	// Try connecting to an address that doesn't exist.
	_, err := privacy.NewPresidioDetector(4, privacy.WithPresidioAddr("127.0.0.1:1"))
	if err == nil {
		t.Fatal("expected error for refused connection")
	}
}

func TestPresidioDetector_DetectLanguage(t *testing.T) {
	// Test through the pipeline — detectLanguage is called internally.
	// Russian text should be detected as "ru".
	addr, cleanup := mockPresidioServer(t, func(data []byte) []byte {
		var req struct {
			Text     string `json:"text"`
			Language string `json:"language"`
		}
		json.Unmarshal(data, &req)
		if req.Language != "ru" {
			t.Errorf("expected language 'ru' for Cyrillic text, got %q", req.Language)
		}
		return []byte("[]")
	})
	defer cleanup()

	det, err := privacy.NewPresidioDetector(4, privacy.WithPresidioAddr(addr))
	if err != nil {
		t.Fatal(err)
	}

	det.Detect(context.Background(), "Привет мир", privacy.ProfileModerate)
}
