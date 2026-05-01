package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/crypto/curve25519"
)

// ─── State file ──────────────────────────────────────────

type cliState struct {
	Gateway      string `json:"gateway"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	UserID       string `json:"user_id"`
	Username     string `json:"username"`
	DeviceID     string `json:"device_id"`
	NodeID       string `json:"node_id"`
	// Ed25519 for signed announcements.
	Ed25519PrivKey string `json:"ed25519_priv_key"`
	Ed25519PubKey  string `json:"ed25519_pub_key"`
	// X25519 for E2E key exchange.
	X25519PrivKey string `json:"x25519_priv_key"`
	X25519PubKey  string `json:"x25519_pub_key"`
}

func stateFile() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".vinctum")
	_ = os.MkdirAll(dir, 0700)
	return filepath.Join(dir, "state.json")
}

func loadState() *cliState {
	data, err := os.ReadFile(stateFile())
	if err != nil {
		return &cliState{Gateway: "http://localhost:8080"}
	}
	var s cliState
	_ = json.Unmarshal(data, &s)
	if s.Gateway == "" {
		s.Gateway = "http://localhost:8080"
	}
	return &s
}

func (s *cliState) save() {
	data, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(stateFile(), data, 0600)
}

// ─── HTTP helpers ────────────────────────────────────────

func (s *cliState) doJSON(method, path string, body any) (map[string]any, error) {
	var reqBody io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, s.Gateway+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connection error: %w", err)
	}
	defer resp.Body.Close()

	respData, _ := io.ReadAll(resp.Body)
	var result map[string]any
	_ = json.Unmarshal(respData, &result)

	if resp.StatusCode >= 400 {
		errMsg := string(respData)
		if e, ok := result["error"]; ok {
			errMsg = fmt.Sprintf("%v", e)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, errMsg)
	}

	return result, nil
}

// ─── Commands ────────────────────────────────────────────

func cmdRegister(s *cliState, args []string) {
	if len(args) < 3 {
		fmt.Println("usage: vinctum register <username> <email> <password>")
		return
	}
	resp, err := s.doJSON("POST", "/api/v1/auth/register", map[string]string{
		"username": args[0], "email": args[1], "password": args[2],
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("registered: user_id=%v username=%v\n", resp["user_id"], resp["username"])
}

func cmdLogin(s *cliState, args []string) {
	if len(args) < 2 {
		fmt.Println("usage: vinctum login <email> <password>")
		return
	}
	resp, err := s.doJSON("POST", "/api/v1/auth/login", map[string]string{
		"email": args[0], "password": args[1],
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	s.AccessToken = str(resp, "access_token")
	s.RefreshToken = str(resp, "refresh_token")
	if user, ok := resp["user"].(map[string]any); ok {
		s.UserID = str(user, "user_id")
		s.Username = str(user, "username")
	}
	s.save()
	fmt.Printf("logged in as %s (user_id=%s)\n", s.Username, s.UserID)
}

func cmdSetup(s *cliState, args []string) {
	if s.AccessToken == "" {
		fmt.Println("error: login first")
		return
	}

	deviceName := "cli"
	if len(args) > 0 {
		deviceName = args[0]
	}

	// 1. Generate Ed25519 keypair for signed announcements.
	edPub, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	s.Ed25519PubKey = base64.StdEncoding.EncodeToString(edPub)
	s.Ed25519PrivKey = base64.StdEncoding.EncodeToString(edPriv)

	// 2. Generate X25519 keypair for E2E encryption.
	var x25519Priv [32]byte
	_, _ = rand.Read(x25519Priv[:])
	x25519Pub, _ := curve25519.X25519(x25519Priv[:], curve25519.Basepoint)
	s.X25519PrivKey = base64.StdEncoding.EncodeToString(x25519Priv[:])
	s.X25519PubKey = base64.StdEncoding.EncodeToString(x25519Pub)

	// 3. Register device.
	resp, err := s.doJSON("POST", "/api/v1/devices", map[string]any{
		"name":        deviceName,
		"device_type": "DEVICE_TYPE_DESKTOP",
	})
	if err != nil {
		fmt.Println("error registering device:", err)
		return
	}
	if device, ok := resp["device"].(map[string]any); ok {
		s.DeviceID = str(device, "device_id")
	}
	s.NodeID = s.UserID + ":" + s.DeviceID
	fmt.Printf("device registered: device_id=%s node_id=%s\n", s.DeviceID, s.NodeID)

	// 4. Upload X25519 public key for E2E.
	_, err = s.doJSON("POST", "/api/v1/devices/"+s.DeviceID+"/key", map[string]any{
		"kex_algo":      "x25519",
		"kex_public_key": s.X25519PubKey,
	})
	if err != nil {
		fmt.Println("warning: could not upload device key:", err)
	} else {
		fmt.Println("device key uploaded (X25519)")
	}

	s.save()
	fmt.Println("setup complete!")
}

func cmdSend(s *cliState, args []string) {
	if len(args) < 2 {
		fmt.Println("usage: vinctum send <receiver_node_id> <file_path>")
		return
	}
	if s.AccessToken == "" || s.NodeID == "" {
		fmt.Println("error: login and setup first")
		return
	}

	receiverNodeID := args[0]
	filePath := args[1]

	// Read file.
	fileData, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Println("error reading file:", err)
		return
	}

	// Content hash (SHA-256 of plaintext).
	hash := sha256.Sum256(fileData)
	contentHash := hex.EncodeToString(hash[:])

	// For demo: skip full ECDH key exchange, send plaintext chunks.
	// In production, client would do: generate ephemeral X25519 keypair,
	// fetch receiver's X25519 pubkey, derive shared secret via ECDH+HKDF,
	// encrypt each chunk with AES-256-GCM.
	// Here we just use zeros as ephemeral key placeholder.
	ephemeralPub := make([]byte, 32)
	_, _ = rand.Read(ephemeralPub)

	chunkSize := 256 * 1024 // 256 KB
	totalSize := len(fileData)

	// 1. Initiate transfer.
	resp, err := s.doJSON("POST", "/api/v1/transfers", map[string]any{
		"sender_node_id":         s.NodeID,
		"receiver_node_id":       receiverNodeID,
		"filename":               filepath.Base(filePath),
		"total_size_bytes":       totalSize,
		"content_hash":           contentHash,
		"chunk_size_bytes":       chunkSize,
		"sender_ephemeral_pubkey": base64.StdEncoding.EncodeToString(ephemeralPub),
	})
	if err != nil {
		fmt.Println("error initiating transfer:", err)
		return
	}

	transferID := str(resp, "transfer_id")
	totalChunks, _ := strconv.Atoi(fmt.Sprintf("%v", resp["total_chunks"]))
	fmt.Printf("transfer initiated: id=%s chunks=%d\n", transferID, totalChunks)

	// 2. Upload chunks.
	for i := 0; i < totalChunks; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > totalSize {
			end = totalSize
		}
		chunk := fileData[start:end]

		chunkHash := sha256.Sum256(chunk)
		chunkHashHex := hex.EncodeToString(chunkHash[:])

		err := uploadChunk(s, transferID, i, chunk, chunkHashHex)
		if err != nil {
			fmt.Printf("error uploading chunk %d: %v\n", i, err)
			return
		}
		fmt.Printf("  chunk %d/%d uploaded\n", i+1, totalChunks)
	}

	fmt.Printf("file sent: %s (%d bytes)\n", filepath.Base(filePath), totalSize)
}

func uploadChunk(s *cliState, transferID string, index int, data []byte, hash string) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chunk_index", strconv.Itoa(index))
	_ = w.WriteField("chunk_hash", hash)
	fw, _ := w.CreateFormFile("data", "chunk")
	_, _ = fw.Write(data)
	w.Close()

	req, _ := http.NewRequest("POST", s.Gateway+"/api/v1/chunks/"+transferID, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+s.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	return nil
}

func cmdReceive(s *cliState, args []string) {
	if len(args) < 2 {
		fmt.Println("usage: vinctum receive <transfer_id> <output_path>")
		return
	}
	if s.AccessToken == "" || s.NodeID == "" {
		fmt.Println("error: login and setup first")
		return
	}

	transferID := args[0]
	outPath := args[1]

	// Download chunks via NDJSON stream.
	url := fmt.Sprintf("%s/api/v1/chunks/%s?receiver_node_id=%s", s.Gateway, transferID, s.NodeID)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+s.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("error: HTTP %d: %s\n", resp.StatusCode, body)
		return
	}

	outFile, err := os.Create(outPath)
	if err != nil {
		fmt.Println("error creating output file:", err)
		return
	}
	defer outFile.Close()

	decoder := json.NewDecoder(resp.Body)
	chunkCount := 0
	for decoder.More() {
		var chunk map[string]any
		if err := decoder.Decode(&chunk); err != nil {
			break
		}
		dataB64 := str(chunk, "data")
		data, _ := base64.StdEncoding.DecodeString(dataB64)
		_, _ = outFile.Write(data)
		chunkCount++
		isLast, _ := chunk["isLast"].(bool)
		if !isLast {
			isLast, _ = chunk["is_last"].(bool)
		}
		fmt.Printf("  chunk %d downloaded\n", chunkCount)
		if isLast {
			break
		}
	}

	fmt.Printf("file saved to %s (%d chunks)\n", outPath, chunkCount)
}

func cmdTransfers(s *cliState, _ []string) {
	if s.AccessToken == "" || s.NodeID == "" {
		fmt.Println("error: login and setup first")
		return
	}

	resp, err := s.doJSON("GET", "/api/v1/node-transfers/"+s.NodeID, nil)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	transfers, _ := resp["transfers"].([]any)
	if len(transfers) == 0 {
		fmt.Println("no transfers found")
		return
	}

	for _, t := range transfers {
		tr, _ := t.(map[string]any)
		fmt.Printf("  %s | %s -> %s | %s | %s | %v%%\n",
			str(tr, "transfer_id"),
			str(tr, "sender_node_id"),
			str(tr, "receiver_node_id"),
			str(tr, "filename"),
			str(tr, "status"),
			tr["progress_percent"],
		)
	}
}

func cmdStatus(s *cliState, _ []string) {
	fmt.Printf("gateway:   %s\n", s.Gateway)
	fmt.Printf("user:      %s (%s)\n", s.Username, s.UserID)
	fmt.Printf("device:    %s\n", s.DeviceID)
	fmt.Printf("node_id:   %s\n", s.NodeID)
	fmt.Printf("logged_in: %v\n", s.AccessToken != "")
}

func cmdGateway(s *cliState, args []string) {
	if len(args) < 1 {
		fmt.Println("usage: vinctum gateway <url>")
		fmt.Println("example: vinctum gateway https://abc123.ngrok.io")
		return
	}
	s.Gateway = strings.TrimRight(args[0], "/")
	s.save()
	fmt.Println("gateway set to:", s.Gateway)
}

// ─── Main ────────────────────────────────────────────────

func main() {
	if len(os.Args) < 2 {
		printHelp()
		return
	}

	s := loadState()
	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "gateway":
		cmdGateway(s, args)
	case "register":
		cmdRegister(s, args)
	case "login":
		cmdLogin(s, args)
	case "setup":
		cmdSetup(s, args)
	case "send":
		cmdSend(s, args)
	case "receive":
		cmdReceive(s, args)
	case "transfers":
		cmdTransfers(s, args)
	case "status":
		cmdStatus(s, args)
	default:
		fmt.Printf("unknown command: %s\n\n", cmd)
		printHelp()
	}
}

func printHelp() {
	fmt.Println(`vinctum - Decentralized Data Courier CLI

commands:
  gateway  <url>                          set gateway URL (default: http://localhost:8080)
  register <username> <email> <password>  create account
  login    <email> <password>             authenticate
  setup    [device_name]                  register device + generate keys
  send     <receiver_node_id> <file>      send a file
  receive  <transfer_id> <output_path>    download a file
  transfers                               list your transfers
  status                                  show current state`)
}

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}
