#!/bin/sh
# Build heifbox, run its tests, and show it reading every fixture in the
# repository.
#
# Portable: it needs a Go toolchain and the source in this directory. If
# ImageMagick and an AVIF-capable ffmpeg are present the last section regenerates
# a fixture and compares the reader's answer against them; otherwise that section
# says so and moves on.

set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$here"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say() {
    printf '\n=== %s ===\n' "$1"
}

if command -v go >/dev/null 2>&1; then
    GO=go
elif [ -x /usr/local/go/bin/go ]; then
    GO=/usr/local/go/bin/go
else
    echo "no Go toolchain found on PATH" >&2
    exit 1
fi

say "toolchain"
"$GO" version

say "build"
"$GO" build ./...
echo "build ok"

say "vet"
"$GO" vet ./...
echo "vet ok"

say "gofmt"
unformatted=$(gofmt -l . 2>/dev/null || true)
if [ -n "$unformatted" ]; then
    echo "these files are not gofmt-clean:"
    echo "$unformatted"
    exit 1
fi
echo "gofmt clean"

say "tests"
"$GO" test ./...

say "tests under the race detector"
"$GO" test -race ./...

say "build the command-line tool"
"$GO" build -o "$tmp/heifbox" ./cmd/heifbox
echo "built $tmp/heifbox"

for fixture in testdata/*.avif; do
    say "reading $fixture"
    "$tmp/heifbox" -file "$fixture"
done

# If another implementation is available, check the reader against it. This is
# the section that makes the dimensions in the README reproducible rather than
# merely asserted.
have_identify=0
command -v identify >/dev/null 2>&1 && have_identify=1
have_convert=0
command -v convert >/dev/null 2>&1 && have_convert=1

if [ "$have_identify" -eq 1 ] && [ "$have_convert" -eq 1 ]; then
    say "cross-checking against ImageMagick"

    # Regenerate a fixture at a size no other fixture uses, so the comparison
    # cannot pass by landing on a value one of them already has.
    fresh="$tmp/fresh.avif"
    if convert -size 200x150 gradient:red-blue "$fresh" 2>/dev/null && [ -s "$fresh" ]; then
        reported=$("$tmp/heifbox" -file "$fresh" -json 2>/dev/null |
            tr -d ' \n' | sed -n 's/.*"width":\([0-9]*\),"height":\([0-9]*\).*/\1x\2/p')
        other=$(identify -format '%wx%h' "$fresh" 2>/dev/null || true)
        echo "heifbox reports : ${reported:-nothing}"
        echo "ImageMagick says: ${other:-nothing}"
        if [ -n "$reported" ] && [ "$reported" = "$other" ]; then
            echo "the two agree"
        else
            echo "THE TWO DISAGREE" >&2
            exit 1
        fi
    else
        echo "ImageMagick could not write an AVIF here; skipping the fresh comparison"
    fi

    say "checking every committed fixture against ImageMagick"
    for fixture in testdata/*.avif; do
        reported=$("$tmp/heifbox" -file "$fixture" -json 2>/dev/null |
            tr -d ' \n' | sed -n 's/.*"width":\([0-9]*\),"height":\([0-9]*\).*/\1x\2/p')
        other=$(identify -format '%wx%h' "$fixture" 2>/dev/null || true)
        printf '  %-24s heifbox=%-10s identify=%-10s %s\n' \
            "$fixture" "${reported:-none}" "${other:-none}" \
            "$([ "$reported" = "$other" ] && echo agree || echo DISAGREE)"
        if [ -n "$reported" ] && [ -n "$other" ] && [ "$reported" != "$other" ]; then
            exit 1
        fi
    done
else
    say "cross-checking against ImageMagick"
    echo "ImageMagick is not installed, so the comparison could not run. Install"
    echo "imagemagick with AVIF support to reproduce the dimension checks. The"
    echo "tests still ran and the fixtures were still read."
fi

say "done"
echo "everything that could run, ran."
