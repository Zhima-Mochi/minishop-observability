#!/usr/bin/env bash
set -euo pipefail

# Configuration (override via environment variables)
BASE_URL=${BASE_URL:-http://localhost:8080}
CONCURRENCY=${CONCURRENCY:-10}
REQUESTS=${REQUESTS:-100}
ORDER_PAYLOAD_TEMPLATE=${ORDER_PAYLOAD_TEMPLATE:-}
PAYMENT_PAYLOAD_TEMPLATE=${PAYMENT_PAYLOAD_TEMPLATE:-}

# Ensure required tools are available
for tool in curl jq; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "$tool is required" >&2
    exit 1
  fi
done

random_suffix() {
  printf "%04x%04x" "$RANDOM" "$RANDOM"
}

PRODUCT_ID=${PRODUCT_ID:-prod-$(random_suffix)}
export BASE_URL CONCURRENCY REQUESTS ORDER_PAYLOAD_TEMPLATE PAYMENT_PAYLOAD_TEMPLATE PRODUCT_ID

log() {
  printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*" >&2
}

make_order_payload() {
  if [[ -n "$ORDER_PAYLOAD_TEMPLATE" && -f "$ORDER_PAYLOAD_TEMPLATE" ]]; then
    cat "$ORDER_PAYLOAD_TEMPLATE"
  else
    local customer_id="cust-$(random_suffix)"
    cat <<JSON
{
  "customer_id": "${customer_id}",
  "product_id": "${PRODUCT_ID}",
  "quantity": 1,
  "amount": 1000
}
JSON
  fi
}

make_payment_payload() {
  local order_id=$1
  if [[ -n "$PAYMENT_PAYLOAD_TEMPLATE" && -f "$PAYMENT_PAYLOAD_TEMPLATE" ]]; then
    cat "$PAYMENT_PAYLOAD_TEMPLATE"
  else
    if [[ -z "$order_id" ]]; then
      order_id="order-$(random_suffix)"
    fi
    cat <<JSON
{
  "order_id": "${order_id}",
  "amount": 1000
}
JSON
  fi
}

# invoke_api METHOD PATH BODY -> sets global REPLY_STATUS and REPLY_BODY
invoke_api() {
  local method=$1
  local path=$2
  local body=$3
  local response
  response=$(curl -s -w '\n%{http_code}' -X "$method" \
    -H 'Content-Type: application/json' \
    -d "$body" \
    "${BASE_URL}${path}")
  REPLY_STATUS=$(echo "$response" | tail -n1)
  REPLY_BODY=$(echo "$response" | sed '$d')
  log "$method ${path} -> ${REPLY_STATUS}"
}

worker() {
  for ((i = 0; i < REQUESTS; i++)); do
    local order_payload order_id

    order_payload=$(make_order_payload)
    invoke_api POST /order "$order_payload"
    if [[ "${REPLY_STATUS:0:1}" != "2" ]]; then
      log "Order failed (status $REPLY_STATUS)"
      continue
    fi
    order_id=$(echo "$REPLY_BODY" | jq -r '.order_id // empty')

    local payment_payload
    payment_payload=$(make_payment_payload "$order_id")
    invoke_api POST /payment/pay "$payment_payload"
    if [[ "${REPLY_STATUS:0:1}" != "2" ]]; then
      log "Payment failed (status $REPLY_STATUS)"
      continue
    fi
  done
}

export -f worker invoke_api log make_order_payload make_payment_payload random_suffix

seq "$CONCURRENCY" | xargs -I{} -n1 -P"$CONCURRENCY" bash -c worker
