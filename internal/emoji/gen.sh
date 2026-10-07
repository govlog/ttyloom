#!/usr/bin/env bash
# Regenerate emoji.txt from Unicode Emoji 17.0 (Unicode-3.0).
# The original notice is in ../../licenses/unicode/LICENSE.txt.
set -euo pipefail
cd "$(dirname "$0")"

curl -fsSL https://unicode.org/Public/17.0.0/emoji/emoji-test.txt | awk '
/^# group: / { group = substr($0, 10); next }
/; fully-qualified/ {
	hashpos = index($0, "#")
	if (hashpos == 0) next
	rest = substr($0, hashpos + 2)
	n = split(rest, parts, " ")
	emoji = parts[1]
	name = parts[3]
	for (i = 4; i <= n; i++) name = name " " parts[i]
	if (index(name, "skin tone") > 0) next
	print emoji "\t" name "\t" group
}
' > emoji.txt

wc -l emoji.txt

# Regenerate aliases.txt from gemoji v4.1.0 (MIT): the GitHub/Slack
# shortcodes of each emoji (":+1:", ":tada:"), for the ":" completion.
# The original notice is in ../../licenses/gemoji/LICENSE.
curl -fsSL https://raw.githubusercontent.com/github/gemoji/v4.1.0/db/emoji.json |
	jq -r '.[] | select(.emoji and (.aliases | length > 0)) | "\(.emoji)\t\(.aliases | join(" "))"' > aliases.txt

wc -l aliases.txt
