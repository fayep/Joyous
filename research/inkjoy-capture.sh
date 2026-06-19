#!/bin/bash
# inkjoy-capture.sh — InkJoy MQTT capture for EdgeRouter X
#
# Usage (on the router):
#   sudo bash inkjoy-capture.sh          # rolling pcap: 1h files, 24h retention, gzipped
#   sudo bash inkjoy-capture.sh --live   # print decoded MQTT JSON inline
#
# Copy captures off the router:
#   scp "ubnt@192.168.1.1:/tmp/inkjoy_*.pcap.gz" ~/

IFACE=eth4
MQTT_HOST=13.39.148.101
MQTT_PORT=1883
FILTER="host ${MQTT_HOST} and port ${MQTT_PORT}"

CAPDIR=/tmp           # tmpfs on ERX — RAM-backed, lost on reboot; use /mnt/usb0 if you have a USB drive
PREFIX=inkjoy_mqtt
ROTATE_SECS=3600      # new file every hour
KEEP_HOURS=24         # delete gz files older than this

MODE=pcap
[[ "$1" == "--live" ]] && MODE=live

# ── Live mode ──────────────────────────────────────────────────────────────

if [[ $MODE == live ]]; then
    echo "Live MQTT decode on ${IFACE} → ${MQTT_HOST}:${MQTT_PORT}"
    echo "Press Ctrl-C to stop."
    echo ""
    tcpdump -i "${IFACE}" -s0 -A -l "${FILTER}" 2>/dev/null | \
    awk '
        /\/(device|inkjoyap)\// {
            match($0, /\/(device|inkjoyap)\/[^ .]+/, arr)
            printf "\n--- %s ---\n", arr[0]
            next
        }
        /"action"/ { print }
    '
    exit 0
fi

# ── Rolling pcap mode ──────────────────────────────────────────────────────

echo "InkJoy MQTT capture — rolling ${ROTATE_SECS}s files, ${KEEP_HOURS}h retention"
echo "Interface: ${IFACE}   Filter: ${FILTER}"
echo "Output dir: ${CAPDIR}/${PREFIX}_YYYYMMDD_HHMMSS.pcap"
echo "Press Ctrl-C to stop."
echo ""

# Start tcpdump in background with hourly file rotation
tcpdump -i "${IFACE}" -s0 -U \
    -G "${ROTATE_SECS}" \
    -w "${CAPDIR}/${PREFIX}_%Y%m%d_%H%M%S.pcap" \
    "${FILTER}" 2>/dev/null &
TCPDUMP_PID=$!

echo "tcpdump PID ${TCPDUMP_PID} started."
echo ""

# Trap Ctrl-C so we clean up tcpdump and gzip the last file
cleanup() {
    echo ""
    echo "Stopping capture (PID ${TCPDUMP_PID})..."
    kill "${TCPDUMP_PID}" 2>/dev/null
    wait "${TCPDUMP_PID}" 2>/dev/null

    # Compress any remaining uncompressed files
    find "${CAPDIR}" -name "${PREFIX}_*.pcap" ! -name "*.gz" | while read -r f; do
        echo "Compressing ${f}..."
        gzip -f "${f}"
    done
    echo "Done."
    exit 0
}
trap cleanup INT TERM

# Maintenance loop: runs every 5 minutes
#   - gzip any pcap files that tcpdump has finished with (older than ROTATE_SECS + 2 min buffer)
#   - delete gz files beyond the retention window
GZIP_AFTER_MINS=$(( (ROTATE_SECS / 60) + 2 ))
KEEP_MINS=$(( KEEP_HOURS * 60 ))

while kill -0 "${TCPDUMP_PID}" 2>/dev/null; do
    sleep 300

    # Gzip closed pcap files (tcpdump has moved on to a newer file)
    find "${CAPDIR}" -name "${PREFIX}_*.pcap" ! -name "*.gz" \
         -mmin "+${GZIP_AFTER_MINS}" | while read -r f; do
        gzip -f "${f}" && echo "[$(date '+%H:%M:%S')] Compressed: $(basename "${f}").gz"
    done

    # Prune gz files older than retention window
    find "${CAPDIR}" -name "${PREFIX}_*.pcap.gz" \
         -mmin "+${KEEP_MINS}" | while read -r f; do
        rm -f "${f}" && echo "[$(date '+%H:%M:%S')] Pruned: $(basename "${f}")"
    done
done

wait "${TCPDUMP_PID}"
