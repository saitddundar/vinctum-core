package p2p

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/rs/zerolog/log"
	p2pv1 "github.com/saitddundar/vinctum-core/proto/p2p/v1"
	"github.com/saitddundar/vinctum-core/services/transfer/storage"
	"google.golang.org/protobuf/encoding/protodelim"
	"google.golang.org/protobuf/proto"
)

const TransferProtocolID protocol.ID = "/vinctum/transfer/1.0.0"

// ChunkReceivedCallback is called when a chunk arrives via P2P.
type ChunkReceivedCallback func(transferID string, chunkIndex, totalChunks int32)

// TransferProtocol handles direct P2P file transfer over libp2p streams.
type TransferProtocol struct {
	host            host.Host
	chunks          storage.ChunkStore
	onChunkReceived ChunkReceivedCallback
}

// NewTransferProtocol creates a new transfer protocol handler.
func NewTransferProtocol(h host.Host, cs storage.ChunkStore, cb ChunkReceivedCallback) *TransferProtocol {
	return &TransferProtocol{
		host:            h,
		chunks:          cs,
		onChunkReceived: cb,
	}
}

// RegisterHandler registers the stream handler on the host so this node
// can receive incoming P2P transfers.
func (tp *TransferProtocol) RegisterHandler() {
	tp.host.SetStreamHandler(TransferProtocolID, tp.handleStream)
	log.Info().Str("protocol", string(TransferProtocolID)).Msg("p2p transfer protocol registered")
}

// handleStream processes an incoming P2P transfer stream from a sender.
func (tp *TransferProtocol) handleStream(s network.Stream) {
	defer s.Close()

	remotePeer := s.Conn().RemotePeer()
	log.Info().Str("peer", remotePeer.String()).Msg("incoming p2p transfer stream")

	reader := bufio.NewReaderSize(s, 64*1024)
	unmarshalOpts := protodelim.UnmarshalOptions{MaxSize: 10 << 20}

	// 1. Read transfer header.
	var header p2pv1.P2PTransferHeader
	if err := unmarshalOpts.UnmarshalFrom(reader, &header); err != nil {
		log.Error().Err(err).Msg("failed to read p2p transfer header")
		return
	}

	log.Info().
		Str("transfer_id", header.TransferId).
		Int32("total_chunks", header.TotalChunks).
		Int64("total_size", header.TotalSizeBytes).
		Msg("p2p transfer started")

	// 2. Receive chunks in a loop.
	for {
		var chunk p2pv1.P2PChunk
		if err := unmarshalOpts.UnmarshalFrom(reader, &chunk); err != nil {
			if err == io.EOF {
				break
			}
			log.Error().Err(err).Str("transfer_id", header.TransferId).Msg("failed to read p2p chunk")
			writeAck(s, chunk.ChunkIndex, false, err.Error())
			return
		}

		// Verify chunk hash (over ciphertext).
		if chunk.ChunkHash != "" {
			actual := sha256Hex(chunk.Data)
			if actual != chunk.ChunkHash {
				log.Warn().
					Str("expected", chunk.ChunkHash).
					Str("actual", actual).
					Msg("p2p chunk hash mismatch")
				writeAck(s, chunk.ChunkIndex, false, "chunk hash mismatch")
				return
			}
		}

		// Save chunk to storage.
		if tp.chunks != nil {
			if _, err := tp.chunks.SaveChunk(header.TransferId, chunk.ChunkIndex, chunk.Data); err != nil {
				log.Error().Err(err).Int32("chunk", chunk.ChunkIndex).Msg("failed to save p2p chunk")
				writeAck(s, chunk.ChunkIndex, false, "storage error")
				return
			}
		}

		// Send ACK.
		writeAck(s, chunk.ChunkIndex, true, "")

		// Notify callback.
		if tp.onChunkReceived != nil {
			tp.onChunkReceived(header.TransferId, chunk.ChunkIndex, header.TotalChunks)
		}

		log.Debug().
			Str("transfer_id", header.TransferId).
			Int32("chunk", chunk.ChunkIndex).
			Bool("is_last", chunk.IsLast).
			Msg("p2p chunk received")

		if chunk.IsLast {
			break
		}
	}

	log.Info().Str("transfer_id", header.TransferId).Msg("p2p transfer completed")
}

// SendFile sends all chunks of a transfer directly to a peer via libp2p stream.
// chunkReader returns (data, hash, error) for each chunk index.
func (tp *TransferProtocol) SendFile(
	ctx context.Context,
	peerID peer.ID,
	transferID string,
	totalChunks int32,
	totalSizeBytes int64,
	contentHash string,
	chunkReader func(index int32) (data []byte, hash string, err error),
) error {
	s, err := tp.host.NewStream(ctx, peerID, TransferProtocolID)
	if err != nil {
		return fmt.Errorf("open stream to %s: %w", peerID, err)
	}
	defer s.Close()

	reader := bufio.NewReaderSize(s, 64*1024)
	unmarshalOpts := protodelim.UnmarshalOptions{MaxSize: 1024}

	// 1. Send header.
	header := &p2pv1.P2PTransferHeader{
		TransferId:     transferID,
		TotalChunks:    totalChunks,
		TotalSizeBytes: totalSizeBytes,
		ContentHash:    contentHash,
	}
	if err := writeMsg(s, header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	// 2. Send chunks one by one.
	for i := int32(0); i < totalChunks; i++ {
		data, hash, err := chunkReader(i)
		if err != nil {
			return fmt.Errorf("read chunk %d: %w", i, err)
		}

		chunk := &p2pv1.P2PChunk{
			ChunkIndex: i,
			Data:       data,
			ChunkHash:  hash,
			IsLast:     i == totalChunks-1,
		}
		if err := writeMsg(s, chunk); err != nil {
			return fmt.Errorf("write chunk %d: %w", i, err)
		}

		// 3. Wait for ACK.
		var ack p2pv1.P2PChunkAck
		if err := unmarshalOpts.UnmarshalFrom(reader, &ack); err != nil {
			return fmt.Errorf("read ack for chunk %d: %w", i, err)
		}
		if !ack.Accepted {
			return fmt.Errorf("chunk %d rejected: %s", i, ack.Error)
		}
	}

	return nil
}

// IsReachable checks if a peer is directly reachable.
func (tp *TransferProtocol) IsReachable(ctx context.Context, peerID peer.ID) bool {
	if tp.host.Network().Connectedness(peerID) == network.Connected {
		return true
	}
	addrs := tp.host.Peerstore().Addrs(peerID)
	return len(addrs) > 0
}

// PeerID returns this node's libp2p peer ID.
func (tp *TransferProtocol) PeerID() peer.ID {
	return tp.host.ID()
}

// Addrs returns this node's listening multiaddrs (without peer ID).
func (tp *TransferProtocol) Addrs() []string {
	var addrs []string
	for _, a := range tp.host.Addrs() {
		addrs = append(addrs, a.String())
	}
	return addrs
}

// ─── Wire helpers ──────────────────────────────────────────

func writeMsg(w io.Writer, msg proto.Message) error {
	_, err := protodelim.MarshalTo(w, msg)
	return err
}

func writeAck(s network.Stream, chunkIndex int32, accepted bool, errMsg string) {
	ack := &p2pv1.P2PChunkAck{
		ChunkIndex: chunkIndex,
		Accepted:   accepted,
		Error:      errMsg,
	}
	if err := writeMsg(s, ack); err != nil {
		log.Warn().Err(err).Int32("chunk", chunkIndex).Msg("failed to send p2p ack")
	}
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
