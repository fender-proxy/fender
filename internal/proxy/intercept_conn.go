package proxy

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"net"

	"github.com/fender-proxy/fender/internal/config"
	controlapi "github.com/moby/buildkit/api/services/control"
	"google.golang.org/protobuf/proto"
	"golang.org/x/net/http2/hpack"
)

type interceptConn struct {
	net.Conn
	cfg *config.Config

	isH2         bool
	prefaceSeen  bool
	writeBuf     bytes.Buffer
	hdec         *hpack.Decoder
	currentStreamID uint32

	solveStreamID  uint32
	solveDataBuf   bytes.Buffer
	solveMsgLength uint32
}

func newInterceptConn(conn net.Conn, cfg *config.Config) net.Conn {
	ic := &interceptConn{
		Conn: conn,
		cfg:  cfg,
	}
	ic.hdec = hpack.NewDecoder(4096, func(f hpack.HeaderField) {
		if f.Name == ":path" && f.Value == "/moby.buildkit.v1.Control/Solve" {
			ic.solveStreamID = ic.currentStreamID
			slog.Debug("detected BuildKit /Solve gRPC stream", "streamID", ic.solveStreamID)
		}
	})
	return ic
}

func (c *interceptConn) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}

	// Save the original length for returning to the caller
	n := len(b)

	c.writeBuf.Write(b)

	// 1. Sniff HTTP/2 preface if not yet seen
	if !c.prefaceSeen {
		preface := []byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n")
		buffered := c.writeBuf.Bytes()
		if len(buffered) >= len(preface) {
			if bytes.HasPrefix(buffered, preface) {
				c.isH2 = true
				c.prefaceSeen = true
				// Write the preface to the underlying socket
				if _, err := c.Conn.Write(preface); err != nil {
					return 0, err
				}
				c.writeBuf.Next(len(preface))
			} else {
				// Not HTTP/2, disable interception and flush
				c.isH2 = false
				c.prefaceSeen = true
				if _, err := c.Conn.Write(buffered); err != nil {
					return 0, err
				}
				c.writeBuf.Reset()
				return n, nil
			}
		} else {
			// Wait for more bytes to determine if HTTP/2
			if bytes.HasPrefix(preface, buffered) {
				return n, nil
			}
			// Not HTTP/2
			c.isH2 = false
			c.prefaceSeen = true
			if _, err := c.Conn.Write(buffered); err != nil {
				return 0, err
			}
			c.writeBuf.Reset()
			return n, nil
		}
	}

	if !c.isH2 {
		// Passthrough mode
		buffered := c.writeBuf.Bytes()
		if _, err := c.Conn.Write(buffered); err != nil {
			return 0, err
		}
		c.writeBuf.Reset()
		return n, nil
	}

	// 2. HTTP/2 Frame processing loop
	for {
		buffered := c.writeBuf.Bytes()
		if len(buffered) < 9 {
			break // Need more bytes to parse frame header
		}

		length := uint32(buffered[0])<<16 | uint32(buffered[1])<<8 | uint32(buffered[2])
		frameType := buffered[3]
		flags := buffered[4]
		streamID := binary.BigEndian.Uint32(buffered[5:9]) & 0x7fffffff

		if len(buffered) < 9+int(length) {
			break // Frame payload is incomplete, wait for more writes
		}

		// Consume the full frame
		frameBytes := c.writeBuf.Next(9 + int(length))
		payload := frameBytes[9:]

		// Process specific frames
		switch frameType {
		case 0x1: // HEADERS
			c.currentStreamID = streamID
			headerPayload := payload
			if flags&0x8 != 0 { // PADDED
				padLen := int(headerPayload[0])
				headerPayload = headerPayload[1 : len(headerPayload)-padLen]
			}
			if flags&0x20 != 0 { // PRIORITY
				headerPayload = headerPayload[5:]
			}
			_, _ = c.hdec.Write(headerPayload)

		case 0x9: // CONTINUATION
			c.currentStreamID = streamID
			_, _ = c.hdec.Write(payload)

		case 0x0: // DATA
			if c.solveStreamID != 0 && streamID == c.solveStreamID {
				c.solveDataBuf.Write(payload)

				// A gRPC frame has a 5-byte header: 1-byte compression flag, 4-byte length
				if c.solveMsgLength == 0 && c.solveDataBuf.Len() >= 5 {
					c.solveMsgLength = binary.BigEndian.Uint32(c.solveDataBuf.Bytes()[1:5])
				}

				if c.solveMsgLength > 0 && c.solveDataBuf.Len() >= 5+int(c.solveMsgLength) {
					// We have received the complete SolveRequest gRPC message
					grpcHeader := c.solveDataBuf.Next(5)
					protoBytes := c.solveDataBuf.Next(int(c.solveMsgLength))

					// Parse SolveRequest
					var req controlapi.SolveRequest
					if err := proto.Unmarshal(protoBytes, &req); err == nil {
						slog.Info("intercepted SolveRequest, modifying frontend", "original", req.Frontend)
						
						origFrontend := req.Frontend
						if origFrontend == "dockerfile.v0" || origFrontend == "" {
							req.Frontend = "gateway.v0"
							if req.FrontendAttrs == nil {
								req.FrontendAttrs = make(map[string]string)
							}
							req.FrontendAttrs["source"] = "fender-frontend:local"
							req.FrontendAttrs["fender-original-frontend"] = "docker/dockerfile:1"
						} else if origFrontend == "gateway.v0" {
							originalSource := req.FrontendAttrs["source"]
							req.FrontendAttrs["fender-original-frontend"] = originalSource
							req.FrontendAttrs["source"] = "fender-frontend:local"
						}

						// Inject mapping rules and authentication credentials
						if req.FrontendAttrs != nil {
							req.FrontendAttrs["fender-default-registry"] = c.cfg.DefaultRegistry.Name
							regMap := make(map[string]string, len(c.cfg.RegistryMap))
							for k, v := range c.cfg.RegistryMap {
								regMap[k] = v.Name
							}
							regMapJSON, _ := json.Marshal(regMap)
							req.FrontendAttrs["fender-registry-map"] = string(regMapJSON)
						}

						// Re-serialize protobuf
						newProtoBytes, err := proto.Marshal(&req)
						if err == nil {
							// Update gRPC header length
							binary.BigEndian.PutUint32(grpcHeader[1:5], uint32(len(newProtoBytes)))

							// Construct new HTTP/2 DATA frame payload
							var newDataPayload bytes.Buffer
							newDataPayload.Write(grpcHeader)
							newDataPayload.Write(newProtoBytes)

							// Append remaining unread DATA bytes on the stream (if any)
							if c.solveDataBuf.Len() > 0 {
								newDataPayload.Write(c.solveDataBuf.Bytes())
							}

							// Reconstruct the HTTP/2 DATA frame header
							newLength := newDataPayload.Len()
							newFrame := make([]byte, 9+newLength)
							newFrame[0] = byte(newLength >> 16)
							newFrame[1] = byte(newLength >> 8)
							newFrame[2] = byte(newLength)
							newFrame[3] = 0x0 // DATA type
							newFrame[4] = flags
							binary.BigEndian.PutUint32(newFrame[5:9], streamID)
							copy(newFrame[9:], newDataPayload.Bytes())

							// Write the modified frame to the underlying connection
							if _, err := c.Conn.Write(newFrame); err != nil {
								return 0, err
							}

							// Disable interception for this stream
							c.solveStreamID = 0
							c.solveDataBuf.Reset()
							c.solveMsgLength = 0
							continue
						}
					}
					
					// If parsing or marshaling fails, fall back to write original frame
					slog.Warn("failed to mutate SolveRequest protobuf, falling back to passthrough")
					c.solveStreamID = 0
					c.solveDataBuf.Reset()
					c.solveMsgLength = 0
				} else {
					// We need more DATA frames to assemble the complete gRPC message.
					// We do NOT write this DATA frame to the upstream connection yet,
					// because we are buffering its payload to edit it!
					continue
				}
			}
		}

		// Write the original frame to the underlying connection
		if _, err := c.Conn.Write(frameBytes); err != nil {
			return 0, err
		}
	}

	return n, nil
}
