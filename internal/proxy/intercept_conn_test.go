package proxy

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/fender-proxy/fender/internal/config"
	controlapi "github.com/moby/buildkit/api/services/control"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/protobuf/proto"
)

func TestInterceptConn_SolveRequest(t *testing.T) {
	// 1. Setup config
	cfg := &config.Config{
		DefaultRegistry: config.RegistryConfig{
			Name: "myregistry.com",
		},
		RegistryMap: map[string]config.RegistryConfig{
			"docker.io": {Name: "nexus.corp/docker"},
		},
	}

	// 2. Setup mock net.Conn pipe
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	intercepted := newInterceptConn(serverConn, cfg)

	// Channel to receive the modified bytes written by the interceptor to the mock serverConn
	done := make(chan struct{})
	var resultBytes bytes.Buffer
	go func() {
		defer close(done)
		// Read all data written to serverConn (which emerges from clientConn)
		buf := make([]byte, 4096)
		for {
			_ = clientConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			n, err := clientConn.Read(buf)
			if n > 0 {
				resultBytes.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()

	// 3. Write HTTP/2 connection preface
	preface := []byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n")
	if _, err := intercepted.Write(preface); err != nil {
		t.Fatalf("failed to write preface: %v", err)
	}

	// 4. Construct HTTP/2 HEADERS frame with path /moby.buildkit.v1.Control/Solve
	var headersBuf bytes.Buffer
	henc := hpack.NewEncoder(&headersBuf)
	_ = henc.WriteField(hpack.HeaderField{Name: ":path", Value: "/moby.buildkit.v1.Control/Solve"})
	_ = henc.WriteField(hpack.HeaderField{Name: ":method", Value: "POST"})

	// Write HEADERS frame (stream ID = 3, type = 1, flags = 4 (END_HEADERS))
	streamID := uint32(3)
	headerFrame := make([]byte, 9+headersBuf.Len())
	newLength := headersBuf.Len()
	headerFrame[0] = byte(newLength >> 16)
	headerFrame[1] = byte(newLength >> 8)
	headerFrame[2] = byte(newLength)
	headerFrame[3] = 0x1 // HEADERS type
	headerFrame[4] = 0x4 // END_HEADERS flag
	binary.BigEndian.PutUint32(headerFrame[5:9], streamID)
	copy(headerFrame[9:], headersBuf.Bytes())

	if _, err := intercepted.Write(headerFrame); err != nil {
		t.Fatalf("failed to write HEADERS frame: %v", err)
	}

	// 5. Construct a gRPC SolveRequest payload
	req := &controlapi.SolveRequest{
		Frontend: "dockerfile.v0",
		FrontendAttrs: map[string]string{
			"filename": "Dockerfile",
		},
	}
	reqBytes, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal SolveRequest: %v", err)
	}

	// Construct gRPC 5-byte header (compression flag = 0, length = len(proto))
	grpcHeader := make([]byte, 5)
	grpcHeader[0] = 0 // uncompressed
	binary.BigEndian.PutUint32(grpcHeader[1:5], uint32(len(reqBytes)))

	var grpcPayload bytes.Buffer
	grpcPayload.Write(grpcHeader)
	grpcPayload.Write(reqBytes)

	// Write DATA frame (stream ID = 3, type = 0, flags = 1 (END_STREAM))
	dataFrameLen := grpcPayload.Len()
	dataFrame := make([]byte, 9+dataFrameLen)
	dataFrame[0] = byte(dataFrameLen >> 16)
	dataFrame[1] = byte(dataFrameLen >> 8)
	dataFrame[2] = byte(dataFrameLen)
	dataFrame[3] = 0x0 // DATA type
	dataFrame[4] = 0x1 // END_STREAM flag
	binary.BigEndian.PutUint32(dataFrame[5:9], streamID)
	copy(dataFrame[9:], grpcPayload.Bytes())

	if _, err := intercepted.Write(dataFrame); err != nil {
		t.Fatalf("failed to write DATA frame: %v", err)
	}

	// Close the client connection to stop the read loop
	clientConn.Close()
	<-done

	// 6. Verify the results
	res := resultBytes.Bytes()
	if !bytes.HasPrefix(res, preface) {
		t.Fatalf("expected result to start with HTTP/2 preface, but got: %q", string(res))
	}

	// Read and parse frames from resultBytes
	framer := http2.NewFramer(io.Discard, bytes.NewReader(res[len(preface):]))
	
	// First frame should be HEADERS
	frame, err := framer.ReadFrame()
	if err != nil {
		t.Fatalf("failed to read first frame: %v", err)
	}
	if frame.Header().Type != http2.FrameHeaders {
		t.Errorf("expected first frame to be HEADERS, got %v", frame.Header().Type)
	}

	// Second frame should be DATA containing the mutated gRPC message
	frame, err = framer.ReadFrame()
	if err != nil {
		t.Fatalf("failed to read second frame: %v", err)
	}
	dataFrameGot, ok := frame.(*http2.DataFrame)
	if !ok {
		t.Fatalf("expected DATA frame, got %T", frame)
	}

	payloadGot := dataFrameGot.Data()
	if len(payloadGot) < 5 {
		t.Fatalf("DATA frame payload too short: %d bytes", len(payloadGot))
	}

	grpcLen := binary.BigEndian.Uint32(payloadGot[1:5])
	if len(payloadGot) != 5+int(grpcLen) {
		t.Fatalf("invalid gRPC payload length, header states %d, got %d", grpcLen, len(payloadGot)-5)
	}

	var mutatedReq controlapi.SolveRequest
	if err := proto.Unmarshal(payloadGot[5:], &mutatedReq); err != nil {
		t.Fatalf("failed to unmarshal mutated SolveRequest: %v", err)
	}

	// Verify the frontend was modified
	if mutatedReq.Frontend != "gateway.v0" {
		t.Errorf("expected frontend to be gateway.v0, got %q", mutatedReq.Frontend)
	}

	// Verify injected frontend options
	if mutatedReq.FrontendAttrs["source"] != "fender-frontend:local" {
		t.Errorf("expected source to be fender-frontend:local, got %q", mutatedReq.FrontendAttrs["source"])
	}
	if mutatedReq.FrontendAttrs["fender-original-frontend"] != "docker/dockerfile:1" {
		t.Errorf("expected original frontend to be docker/dockerfile:1, got %q", mutatedReq.FrontendAttrs["fender-original-frontend"])
	}
	if mutatedReq.FrontendAttrs["fender-default-registry"] != "myregistry.com" {
		t.Errorf("expected default registry to be myregistry.com, got %q", mutatedReq.FrontendAttrs["fender-default-registry"])
	}

	var regMap map[string]string
	if err := json.Unmarshal([]byte(mutatedReq.FrontendAttrs["fender-registry-map"]), &regMap); err != nil {
		t.Fatalf("failed to parse registry map JSON: %v", err)
	}
	if regMap["docker.io"] != "nexus.corp/docker" {
		t.Errorf("expected registry map docker.io to map to nexus.corp/docker, got %q", regMap["docker.io"])
	}
}
