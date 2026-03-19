#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
AGENT_SERVER_DIR="$REPO_ROOT/agent-server"

AGENT_SERVER_PORT="${AGENT_SERVER_PORT:-8080}"
PROXY_PORT="${PROXY_PORT:-3000}"
CONTAINER_NAME="agent-server-test-client"
IMAGE_TAG="$CONTAINER_NAME:local"

# --- Prerequisites ---

for cmd in docker node curl; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "ERROR: $cmd is required but not found in PATH."
    exit 1
  fi
done

# --- Load provider credentials from .env ---

DOTENV_PATH="${AGENT_SERVER_TEST_DOTENV:-$AGENT_SERVER_DIR/.env}"
if [[ ! -f "$DOTENV_PATH" ]]; then
  echo "ERROR: .env file not found at $DOTENV_PATH"
  echo "Set AGENT_SERVER_TEST_DOTENV to override the path."
  exit 1
fi

OPENAI_API_KEY_VALUE=""
ANTHROPIC_API_KEY_VALUE=""

# Map AGENT_SERVER_IT_* keys to runtime provider credential keys.
while IFS= read -r line || [[ -n "$line" ]]; do
  trimmed="${line#"${line%%[![:space:]]*}"}"
  [[ -z "$trimmed" || "$trimmed" == \#* ]] && continue
  key="${trimmed%%=*}"
  value="${trimmed#*=}"
  case "$key" in
    AGENT_SERVER_IT_OPENAI_API_KEY|OPENAI_API_KEY)
      OPENAI_API_KEY_VALUE="$value"
      ;;
    AGENT_SERVER_IT_ANTHROPIC_API_KEY|ANTHROPIC_API_KEY)
      ANTHROPIC_API_KEY_VALUE="$value"
      ;;
  esac
done < "$DOTENV_PATH"

if [[ -z "$OPENAI_API_KEY_VALUE" && -z "$ANTHROPIC_API_KEY_VALUE" ]]; then
  echo "WARNING: No provider API keys found in $DOTENV_PATH"
fi

# --- Cleanup handler ---

PROXY_PID=""
cleanup() {
  echo ""
  echo "Cleaning up..."
  [[ -n "$PROXY_PID" ]] && kill "$PROXY_PID" 2>/dev/null || true
  docker stop "$CONTAINER_NAME" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# --- Build Docker image ---

echo "Building Docker image ($IMAGE_TAG)..."
docker build \
  -t "$IMAGE_TAG" \
  -f "$AGENT_SERVER_DIR/integration/testdata/Dockerfile" \
  "$AGENT_SERVER_DIR"

# --- Start container ---

echo "Starting container ($CONTAINER_NAME) on port $AGENT_SERVER_PORT..."
docker rm -f "$CONTAINER_NAME" 2>/dev/null || true
docker_env_args=()
if [[ -n "$OPENAI_API_KEY_VALUE" ]]; then
  docker_env_args+=(-e "OPENAI_API_KEY=$OPENAI_API_KEY_VALUE")
fi
if [[ -n "$ANTHROPIC_API_KEY_VALUE" ]]; then
  docker_env_args+=(-e "ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY_VALUE")
fi
docker run -d --rm \
  --name "$CONTAINER_NAME" \
  -p "${AGENT_SERVER_PORT}:8080" \
  -e AGENT_SERVER_LISTEN=:8080 \
  -e AGENT_SERVER_STOP_GRACE_TIMEOUT=4s \
  -e AGENT_SERVER_TRAJECTORY_COLLECT_TIMEOUT=8s \
  -e AGENT_SERVER_TRAJECTORY_POLL_INTERVAL=200ms \
  "${docker_env_args[@]}" \
  "$IMAGE_TAG"

# Create default workspace
docker exec "$CONTAINER_NAME" mkdir -p /marginlab/workspaces/test

# --- Wait for health ---

echo "Waiting for agent-server to be healthy..."
HEALTH_URL="http://localhost:${AGENT_SERVER_PORT}/healthz"
DEADLINE=$((SECONDS + 120))
while (( SECONDS < DEADLINE )); do
  if curl -sf "$HEALTH_URL" > /dev/null 2>&1; then
    echo "agent-server is healthy."
    break
  fi
  sleep 1
done
if (( SECONDS >= DEADLINE )); then
  echo "ERROR: agent-server did not become healthy within 120s"
  docker logs "$CONTAINER_NAME" 2>&1 | tail -30
  exit 1
fi

# --- Start proxy ---

echo "Starting proxy on port $PROXY_PORT..."
AGENT_SERVER_UPSTREAM="http://localhost:${AGENT_SERVER_PORT}" \
  PROXY_PORT="$PROXY_PORT" \
  node "$SCRIPT_DIR/proxy.js" &
PROXY_PID=$!

sleep 1

# --- Open browser ---

CLIENT_URL="http://localhost:${PROXY_PORT}"
echo "Opening $CLIENT_URL"
if command -v open &>/dev/null; then
  open "$CLIENT_URL"
elif command -v xdg-open &>/dev/null; then
  xdg-open "$CLIENT_URL"
else
  echo "Open $CLIENT_URL in your browser."
fi

echo ""
echo "Test client running at $CLIENT_URL"
echo "agent-server at http://localhost:${AGENT_SERVER_PORT}"
echo "Press Ctrl+C to stop."
echo ""

wait "$PROXY_PID"
