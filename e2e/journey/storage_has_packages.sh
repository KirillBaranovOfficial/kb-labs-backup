storage="$1"
find "$storage" -mindepth 2 -type f -name package.json -print -quit | grep -q .
