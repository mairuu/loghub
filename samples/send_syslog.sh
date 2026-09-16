#!/bin/sh
# Sends syslog lines to loghub's collector, one message per line, as they are.
#
#   samples/send_syslog.sh                        the files in samples/syslog, over UDP
#   samples/send_syslog.sh --tcp my.log           a file over TCP
#   echo '<134>Aug 20 12:44:56 fw01 action=deny' | samples/send_syslog.sh -
#
# Needs nc from netcat-openbsd.
set -eu

usage() {
	cat <<EOF
usage: $0 [--udp | --tcp] [--host HOST] [--port PORT] [FILE | -]...

Sends each line of each FILE (default: samples/syslog/*.log) to HOST:PORT
(default: 127.0.0.1:514), one UDP datagram per line or one TCP connection
per file. - reads standard input.
EOF
}

proto=udp
host=127.0.0.1
port=514
while [ $# -gt 0 ]; do
	case $1 in
	--udp) proto=udp ;;
	--tcp) proto=tcp ;;
	--host) host=${2:?--host needs a value}; shift ;;
	--port) port=${2:?--port needs a value}; shift ;;
	-h | --help) usage; exit 0 ;;
	--) shift; break ;;
	-?*) usage >&2; exit 2 ;;
	*) break ;;
	esac
	shift
done
if [ $# -eq 0 ]; then
	set -- "$(dirname "$0")"/syslog/*.log
fi

if ! command -v nc >/dev/null; then
	echo "$0: nc is not installed; on Ubuntu, apt-get install netcat-openbsd" >&2
	exit 1
fi

stdin=
trap 'rm -f "$stdin"' EXIT

sent=0
for file in "$@"; do
	if [ "$file" = - ]; then
		# Read once, since the lines are counted as well as sent.
		stdin=$(mktemp)
		cat >"$stdin"
		file=$stdin
	fi
	# Blank lines are dropped by the collector anyway.
	lines=$(grep -c '[^[:space:]]' "$file" || true)
	if [ "$proto" = tcp ]; then
		nc -N "$host" "$port" <"$file"
	else
		while IFS= read -r line || [ -n "$line" ]; do
			if [ -n "$line" ]; then
				printf '%s\n' "$line" | nc -u -q0 "$host" "$port"
			fi
		done <"$file"
	fi
	sent=$((sent + lines))
done
echo "sent $sent line(s) to $host:$port over $proto"
