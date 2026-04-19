#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# Vinctum Core — End-to-End Integration Test
#
# Tests the full flow: register → login → device → session →
# device key → transfer → chunk upload → chunk download
#
# Prerequisites: backend services running, jq installed
# Usage: bash scripts/e2e_test.sh [BASE_URL]
# ─────────────────────────────────────────────────────────────

set -uo pipefail

# Ensure jq is available (Windows: may live in ~/bin as jq.exe)
if ! command -v jq &>/dev/null; then
  if [ -f "$HOME/bin/jq.exe" ]; then
    export PATH="$HOME/bin:$PATH"
    shopt -s expand_aliases 2>/dev/null
    alias jq='jq.exe'
  else
    echo "ERROR: jq is required. Install it: winget install jqlang.jq" >&2
    exit 1
  fi
fi

BASE="${1:-http://localhost:8080}"
PASS=0
FAIL=0
TOTAL=0

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

# ── Helpers ──────────────────────────────────────────────────

rand_suffix() { head -c 4 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

assert() {
  local name="$1" condition="$2"
  TOTAL=$((TOTAL + 1))
  if eval "$condition"; then
    PASS=$((PASS + 1))
    echo -e "  ${GREEN}✓${NC} $name"
  else
    FAIL=$((FAIL + 1))
    echo -e "  ${RED}✗${NC} $name"
  fi
}

post() { curl -s -X POST "$BASE$1" -H "Content-Type: application/json" ${3:+-H "Authorization: Bearer $3"} -d "$2" 2>/dev/null || echo '{}'; }
get()  { curl -s "$BASE$1" ${2:+-H "Authorization: Bearer $2"} 2>/dev/null || echo '{}'; }
del()  { curl -s -X DELETE "$BASE$1" -H "Authorization: Bearer $2" 2>/dev/null || echo '{}'; }
put()  { curl -s -X PUT "$BASE$1" -H "Content-Type: application/json" -H "Authorization: Bearer $2" -d "$3" 2>/dev/null || echo '{}'; }

SUFFIX=$(rand_suffix)

# ─────────────────────────────────────────────────────────────
echo -e "\n${CYAN}═══════════════════════════════════════════════${NC}"
echo -e "${CYAN}  Vinctum Core — End-to-End Test${NC}"
echo -e "${CYAN}  Base URL: ${BASE}${NC}"
echo -e "${CYAN}═══════════════════════════════════════════════${NC}\n"

# ── 1. Health Check ──────────────────────────────────────────
echo -e "${YELLOW}▸ Health Check${NC}"
HEALTH=$(get "/health")
assert "Gateway is healthy" '[ "$(echo "$HEALTH" | jq -r .healthy)" = "true" ]'

# ── 2. Register Users ───────────────────────────────────────
echo -e "\n${YELLOW}▸ Register Users${NC}"

ALICE_REG=$(post "/api/v1/auth/register" "{\"username\":\"alice_$SUFFIX\",\"email\":\"alice_$SUFFIX@test.com\",\"password\":\"Alice123!\"}")
ALICE_UID=$(echo "$ALICE_REG" | jq -r .user_id)
assert "Register Alice" '[ -n "$ALICE_UID" ] && [ "$ALICE_UID" != "null" ]'

BOB_REG=$(post "/api/v1/auth/register" "{\"username\":\"bob_$SUFFIX\",\"email\":\"bob_$SUFFIX@test.com\",\"password\":\"Bob12345!\"}")
BOB_UID=$(echo "$BOB_REG" | jq -r .user_id)
assert "Register Bob" '[ -n "$BOB_UID" ] && [ "$BOB_UID" != "null" ]'

# ── 3. Login ────────────────────────────────────────────────
echo -e "\n${YELLOW}▸ Login${NC}"

ALICE_LOGIN=$(post "/api/v1/auth/login" "{\"email\":\"alice_$SUFFIX@test.com\",\"password\":\"Alice123!\"}")
ALICE_TOKEN=$(echo "$ALICE_LOGIN" | jq -r .access_token)
assert "Alice login → token" '[ -n "$ALICE_TOKEN" ] && [ "$ALICE_TOKEN" != "null" ]'

BOB_LOGIN=$(post "/api/v1/auth/login" "{\"email\":\"bob_$SUFFIX@test.com\",\"password\":\"Bob12345!\"}")
BOB_TOKEN=$(echo "$BOB_LOGIN" | jq -r .access_token)
assert "Bob login → token" '[ -n "$BOB_TOKEN" ] && [ "$BOB_TOKEN" != "null" ]'

# ── 4. Validate Token ──────────────────────────────────────
echo -e "\n${YELLOW}▸ Token Validation${NC}"

VALIDATE=$(post "/api/v1/auth/validate" "{\"token\":\"$ALICE_TOKEN\"}")
assert "Alice token is valid" '[ "$(echo "$VALIDATE" | jq -r .valid)" = "true" ]'

# ── 5. Register Devices ────────────────────────────────────
echo -e "\n${YELLOW}▸ Register Devices${NC}"

ALICE_DEV=$(post "/api/v1/devices" "{\"name\":\"alice-laptop\",\"device_type\":\"DEVICE_TYPE_PC\",\"fingerprint\":\"fp-alice-$SUFFIX\"}" "$ALICE_TOKEN")
ALICE_DEV_ID=$(echo "$ALICE_DEV" | jq -r .device.device_id)
ALICE_NODE_ID=$(echo "$ALICE_DEV" | jq -r .device.node_id)
assert "Alice device registered" '[ -n "$ALICE_DEV_ID" ] && [ "$ALICE_DEV_ID" != "null" ]'
assert "Alice device has node_id" '[ -n "$ALICE_NODE_ID" ] && [ "$ALICE_NODE_ID" != "null" ] && [ "$ALICE_NODE_ID" != "" ]'

BOB_DEV=$(post "/api/v1/devices" "{\"name\":\"bob-phone\",\"device_type\":\"DEVICE_TYPE_PHONE\",\"fingerprint\":\"fp-bob-$SUFFIX\"}" "$BOB_TOKEN")
BOB_DEV_ID=$(echo "$BOB_DEV" | jq -r .device.device_id)
BOB_NODE_ID=$(echo "$BOB_DEV" | jq -r .device.node_id)
assert "Bob device registered" '[ -n "$BOB_DEV_ID" ] && [ "$BOB_DEV_ID" != "null" ]'
assert "Bob device has node_id" '[ -n "$BOB_NODE_ID" ] && [ "$BOB_NODE_ID" != "null" ] && [ "$BOB_NODE_ID" != "" ]'

# ── 6. List & Get Device ───────────────────────────────────
echo -e "\n${YELLOW}▸ Device Operations${NC}"

ALICE_DEVS=$(get "/api/v1/devices" "$ALICE_TOKEN")
DEV_COUNT=$(echo "$ALICE_DEVS" | jq '.devices | length')
assert "Alice can list her devices" '[ "$DEV_COUNT" -ge 1 ]'

ALICE_DEV_GET=$(get "/api/v1/devices/$ALICE_DEV_ID" "$ALICE_TOKEN")
assert "Get device by ID" '[ "$(echo "$ALICE_DEV_GET" | jq -r .device.name)" = "alice-laptop" ]'

# ── 7. Update Device Activity ──────────────────────────────
echo -e "\n${YELLOW}▸ Device Activity${NC}"

ACTIVITY=$(put "/api/v1/devices/$ALICE_DEV_ID/activity" "$ALICE_TOKEN" "{\"node_id\":\"$ALICE_NODE_ID\"}")
assert "Update device activity" '[ "$(echo "$ACTIVITY" | jq -r .success)" = "true" ]'

# ── 8. Upload Device Keys (E2E) ───────────────────────────
echo -e "\n${YELLOW}▸ Device Keys (E2E Key Exchange)${NC}"

# Generate fake 32-byte X25519 keys (base64)
ALICE_PUBKEY=$(head -c 32 /dev/urandom | base64)
BOB_PUBKEY=$(head -c 32 /dev/urandom | base64)

ALICE_KEY_UP=$(post "/api/v1/devices/$ALICE_DEV_ID/key" "{\"kex_algo\":\"x25519\",\"kex_public_key\":\"$ALICE_PUBKEY\"}" "$ALICE_TOKEN")
assert "Alice uploads device key" '[ "$(echo "$ALICE_KEY_UP" | jq -r .key.device_id)" = "$ALICE_DEV_ID" ]'

BOB_KEY_UP=$(post "/api/v1/devices/$BOB_DEV_ID/key" "{\"kex_algo\":\"x25519\",\"kex_public_key\":\"$BOB_PUBKEY\"}" "$BOB_TOKEN")
assert "Bob uploads device key" '[ "$(echo "$BOB_KEY_UP" | jq -r .key.device_id)" = "$BOB_DEV_ID" ]'

# Retrieve keys
ALICE_KEY_GET=$(get "/api/v1/devices/$ALICE_DEV_ID/key" "$ALICE_TOKEN")
assert "Get Alice's device key" '[ "$(echo "$ALICE_KEY_GET" | jq -r .key.kex_algo)" = "x25519" ]'

# ── 9. Create Peer Session ─────────────────────────────────
# NOTE: Peer sessions link devices of the SAME user, not different users.
echo -e "\n${YELLOW}▸ Peer Sessions${NC}"

# Register a second device for Alice
ALICE_DEV2=$(post "/api/v1/devices" "{\"name\":\"alice-phone\",\"device_type\":\"DEVICE_TYPE_PHONE\",\"fingerprint\":\"fp-alice2-$SUFFIX\"}" "$ALICE_TOKEN")
ALICE_DEV2_ID=$(echo "$ALICE_DEV2" | jq -r .device.device_id)

# Upload key for second device
ALICE2_PUBKEY=$(head -c 32 /dev/urandom | base64)
post "/api/v1/devices/$ALICE_DEV2_ID/key" "{\"kex_algo\":\"x25519\",\"kex_public_key\":\"$ALICE2_PUBKEY\"}" "$ALICE_TOKEN" > /dev/null

SESSION=$(post "/api/v1/sessions" "{\"name\":\"test-session-$SUFFIX\",\"device_id\":\"$ALICE_DEV_ID\"}" "$ALICE_TOKEN")
SESSION_ID=$(echo "$SESSION" | jq -r .session.session_id)
assert "Create peer session" '[ -n "$SESSION_ID" ] && [ "$SESSION_ID" != "null" ]'

# Alice's second device joins session
JOIN=$(post "/api/v1/sessions/$SESSION_ID/join" "{\"device_id\":\"$ALICE_DEV2_ID\"}" "$ALICE_TOKEN")
assert "Alice phone joins session" '[ "$(echo "$JOIN" | jq -r .success)" = "true" ]'

# List session devices
SESS_DEVS=$(get "/api/v1/sessions/$SESSION_ID/devices" "$ALICE_TOKEN")
SESS_DEV_COUNT=$(echo "$SESS_DEVS" | jq '.devices | length')
assert "Session has 2 devices" '[ "$SESS_DEV_COUNT" -eq 2 ]'

# Get session device keys
SESS_KEYS=$(get "/api/v1/sessions/$SESSION_ID/keys" "$ALICE_TOKEN")
SESS_KEY_COUNT=$(echo "$SESS_KEYS" | jq '.keys | length')
assert "Session has 2 device keys" '[ "$SESS_KEY_COUNT" -eq 2 ]'

# List sessions
SESSIONS=$(get "/api/v1/sessions" "$ALICE_TOKEN")
assert "List sessions" '[ "$(echo "$SESSIONS" | jq ".sessions | length")" -ge 1 ]'

# ── 10. Initiate Transfer ──────────────────────────────────
echo -e "\n${YELLOW}▸ File Transfer${NC}"

# Create a small test payload (256 bytes)
TEST_DATA=$(head -c 256 /dev/urandom | base64)
CONTENT_HASH=$(echo -n "$TEST_DATA" | sha256sum | cut -d' ' -f1)
EPHEMERAL_KEY=$(head -c 32 /dev/urandom | base64)

TRANSFER=$(post "/api/v1/transfers" "{
  \"sender_node_id\": \"$ALICE_NODE_ID\",
  \"receiver_node_id\": \"$BOB_NODE_ID\",
  \"filename\": \"test-file-$SUFFIX.txt\",
  \"total_size_bytes\": 256,
  \"content_hash\": \"$CONTENT_HASH\",
  \"chunk_size_bytes\": 262144,
  \"replication_factor\": 1,
  \"sender_ephemeral_pubkey\": \"$EPHEMERAL_KEY\"
}" "$ALICE_TOKEN")
TRANSFER_ID=$(echo "$TRANSFER" | jq -r .transfer_id)
TOTAL_CHUNKS=$(echo "$TRANSFER" | jq -r .total_chunks)
assert "Initiate transfer" '[ -n "$TRANSFER_ID" ] && [ "$TRANSFER_ID" != "null" ]'
assert "Transfer has chunks" '[ "$TOTAL_CHUNKS" -ge 1 ]'

# ── 11. Upload Chunk ────────────────────────────────────────
echo -e "\n${YELLOW}▸ Chunk Upload${NC}"

# Write test data to temp file
TMPFILE=$(mktemp)
echo -n "$TEST_DATA" | base64 -d > "$TMPFILE" 2>/dev/null || echo -n "$TEST_DATA" > "$TMPFILE"
CHUNK_HASH=$(sha256sum "$TMPFILE" | cut -d' ' -f1)

UPLOAD=$(curl -sf -X POST "$BASE/api/v1/chunks/$TRANSFER_ID" \
  -H "Authorization: Bearer $ALICE_TOKEN" \
  -F "chunk_index=0" \
  -F "chunk_hash=$CHUNK_HASH" \
  -F "data=@$TMPFILE" 2>/dev/null)
CHUNKS_RECV=$(echo "$UPLOAD" | jq -r .chunks_received)
assert "Upload chunk 0" '[ "$CHUNKS_RECV" -ge 1 ]'
rm -f "$TMPFILE"

# ── 12. Get Transfer Status ────────────────────────────────
echo -e "\n${YELLOW}▸ Transfer Status${NC}"

STATUS=$(get "/api/v1/transfers/$TRANSFER_ID" "$ALICE_TOKEN")
TX_STATUS=$(echo "$STATUS" | jq -r .status)
CHUNKS_DONE=$(echo "$STATUS" | jq -r .chunks_transferred)
assert "Transfer status returned" '[ -n "$TX_STATUS" ] && [ "$TX_STATUS" != "null" ]'
assert "Chunks transferred ≥ 1" '[ "$CHUNKS_DONE" -ge 1 ]'
assert "Ephemeral pubkey stored" '[ "$(echo "$STATUS" | jq -r .sender_ephemeral_pubkey)" != "null" ]'

# ── 13. List Transfers ─────────────────────────────────────
echo -e "\n${YELLOW}▸ List Transfers${NC}"

BOB_TRANSFERS=$(get "/api/v1/node-transfers/$BOB_NODE_ID" "$BOB_TOKEN")
TX_COUNT=$(echo "$BOB_TRANSFERS" | jq '.transfers | length')
assert "Bob can see incoming transfer" '[ "$TX_COUNT" -ge 1 ]'

# ── 14. Download Chunks ────────────────────────────────────
echo -e "\n${YELLOW}▸ Chunk Download${NC}"

DOWNLOAD=$(curl -sf "$BASE/api/v1/chunks/$TRANSFER_ID?receiver_node_id=$BOB_NODE_ID" \
  -H "Authorization: Bearer $BOB_TOKEN" 2>/dev/null)
DL_CHUNK_IDX=$(echo "$DOWNLOAD" | head -1 | jq -r .chunk_index 2>/dev/null)
assert "Bob downloads chunk" '[ "$DL_CHUNK_IDX" = "0" ]'

# ── 15. Cancel Transfer ────────────────────────────────────
echo -e "\n${YELLOW}▸ Cancel Transfer${NC}"

CANCEL=$(post "/api/v1/transfers/$TRANSFER_ID/cancel" "{\"reason\":\"e2e test cleanup\"}" "$ALICE_TOKEN")
assert "Cancel transfer" '[ "$(echo "$CANCEL" | jq -r .success)" = "true" ]'

# ── 16. Leave & Close Session ──────────────────────────────
echo -e "\n${YELLOW}▸ Session Cleanup${NC}"

LEAVE=$(post "/api/v1/sessions/$SESSION_ID/leave" "{\"device_id\":\"$ALICE_DEV2_ID\"}" "$ALICE_TOKEN")
assert "Alice phone leaves session" '[ "$(echo "$LEAVE" | jq -r .success)" = "true" ]'

CLOSE=$(post "/api/v1/sessions/$SESSION_ID/close" "{}" "$ALICE_TOKEN")
assert "Close session" '[ "$(echo "$CLOSE" | jq -r .success)" = "true" ]'

# ── 17. Revoke Device ──────────────────────────────────────
echo -e "\n${YELLOW}▸ Device Cleanup${NC}"

REVOKE=$(del "/api/v1/devices/$ALICE_DEV_ID" "$ALICE_TOKEN")
assert "Revoke Alice's device" '[ "$(echo "$REVOKE" | jq -r .success)" = "true" ]'

# ── 18. Token Refresh ──────────────────────────────────────
echo -e "\n${YELLOW}▸ Token Refresh${NC}"

ALICE_REFRESH_TOKEN=$(echo "$ALICE_LOGIN" | jq -r .refresh_token)
REFRESH=$(post "/api/v1/auth/refresh" "{\"refresh_token\":\"$ALICE_REFRESH_TOKEN\"}")
NEW_TOKEN=$(echo "$REFRESH" | jq -r .access_token)
assert "Token refresh works" '[ -n "$NEW_TOKEN" ] && [ "$NEW_TOKEN" != "null" ]'

# ─────────────────────────────────────────────────────────────
echo -e "\n${CYAN}═══════════════════════════════════════════════${NC}"
if [ "$FAIL" -eq 0 ]; then
  echo -e "${GREEN}  ALL $TOTAL TESTS PASSED ✓${NC}"
else
  echo -e "${RED}  $FAIL/$TOTAL TESTS FAILED${NC}"
  echo -e "${GREEN}  $PASS/$TOTAL TESTS PASSED${NC}"
fi
echo -e "${CYAN}═══════════════════════════════════════════════${NC}\n"

exit "$FAIL"
