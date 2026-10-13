#!/bin/sh
# Runs inside a Debian container that has no D-Bus, no Secret Service and no
# browser, against a Wikilayer server that offers sign-in from another device.
set -eu

server=http://wikilayer:8080
work=$(mktemp -d)
fail() { echo "FAIL: $*" >&2; exit 1; }

go build -o /tmp/wlget /src

echo "== with no keychain, the default store stops wlget and names the file store"
if /tmp/wlget "$server/chat?after_seq=0" 2>"$work/keychain.err"; then
    fail "wlget read with no keychain and no file store"
fi
grep -q "WLGET_STORE=file" "$work/keychain.err" || fail "the refusal does not name WLGET_STORE=file: $(cat "$work/keychain.err")"

echo "== with the file store, wlget signs in from another device on its own"
export WLGET_STORE=file
/tmp/wlget "$server/chat?after_seq=0" >"$work/chat.out" 2>"$work/chat.err" &
reader=$!

for _ in $(seq 60); do
    code=$(grep -o '[BCDFGHJKLMNPQRSTVWXZ]\{4\}-[BCDFGHJKLMNPQRSTVWXZ]\{4\}' "$work/chat.err" | head -n 1 || true)
    [ -n "$code" ] && break
    sleep 1
done
if [ -z "$code" ]; then
    kill -QUIT "$reader" 2>/dev/null || true
    sleep 1
    fail "no code was printed in 60 seconds; where wlget stood, from its goroutine dump: $(cat "$work/chat.err")"
fi
grep -q "no graphical display" "$work/chat.err" || fail "wlget did not say why it chose the code: $(cat "$work/chat.err")"

echo "== a person signs in on the web and approves $code"
jar="$work/cookies"
curl -fsS -c "$jar" -o /dev/null -d username=tester -d password=e2e-password "$server/auth/password/start"
curl -fsS -b "$jar" "$server/device?code=$code" | grep -q "$code" || fail "the approval page does not show the code"
curl -fsS -b "$jar" -d "user_code=$code" -d action=approve "$server/device" | grep -q "Approved" || fail "the approval was not taken"

for _ in $(seq 30); do
    signin=$(ls "$HOME"/.config/wlget/signins/*.json 2>/dev/null | head -n 1 || true)
    [ -n "$signin" ] && break
    sleep 1
done
[ -n "$signin" ] || fail "the sign-in was never kept: $(cat "$work/chat.err")"
[ "$(stat -c %a "$signin")" = 600 ] || fail "the sign-in file is readable by others: $(stat -c %a "$signin")"
[ "$(stat -c %a "$(dirname "$signin")")" = 700 ] || fail "the sign-in folder is open to others"

echo "== the kept sign-in reads the chat"
token=$(grep -o '"access_token":"[^"]*"' "$signin" | cut -d '"' -f 4)
mcp() {
    curl -fsS -D "$work/mcp.headers" -H "Authorization: Bearer $token" \
        -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
        ${session:+-H "Mcp-Session-Id: $session"} -d "$1" "$server/mcp"
}
session=
mcp '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"0"}}}' >/dev/null
session=$(grep -i '^mcp-session-id:' "$work/mcp.headers" | cut -d ' ' -f 2 | tr -d '\r')
mcp '{"jsonrpc":"2.0","method":"notifications/initialized"}' >/dev/null || true
mcp '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"post_chat","arguments":{"session_name":"e2e","body":"hello from the e2e test"}}}' >/dev/null

wait "$reader" || fail "wlget did not finish reading: $(cat "$work/chat.err")"
grep -q "hello from the e2e test" "$work/chat.out" || fail "the message did not arrive: $(cat "$work/chat.out")"

echo "== a second read uses the kept sign-in and asks for no code"
/tmp/wlget "$server/chat?after_seq=0" >"$work/again.out" 2>"$work/again.err"
grep -q "another device" "$work/again.err" && fail "the second read asked for a code again"
grep -q "hello from the e2e test" "$work/again.out" || fail "the second read did not get the message"

echo "== -logout forgets the sign-in"
/tmp/wlget -logout "$server/"
[ ! -e "$signin" ] || fail "-logout left the sign-in file behind"

echo "OK"
