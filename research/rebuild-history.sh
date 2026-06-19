#!/bin/bash
set -euo pipefail

REPO_DIR=~/Downloads/Slam

# Git repo lives on the case-sensitive volume so SlamCamera/ is a real directory
# (not a symlink) and git can track files through it.
JADX_REPO="/Volumes/CaseSensitive"
git() { command git -C "$JADX_REPO" "$@"; }

echo "=== Rebuilding git history from scratch ==="
echo "This will create a new orphan branch 'new-main' with all APKs reprocessed."
echo "Your current 'main' branch will NOT be touched until you confirm at the end."
echo ""

# Check docker image exists
if ! docker image inspect jadx:latest &>/dev/null; then
    echo "ERROR: Docker image 'jadx:latest' not found. Build it first."
    exit 1
fi

# Unpack framework (once) for jadx type resolution
if [ ! -d "$REPO_DIR/.framework" ]; then
    echo "Extracting framework.tar ..."
    mkdir -p "$REPO_DIR/.framework"
    tar -xf "$REPO_DIR/framework.tar" -C "$REPO_DIR/.framework"
fi

# Initialize output git repo on the case-sensitive volume if needed
if [ ! -d "$JADX_REPO/.git" ]; then
    command git init "$JADX_REPO"
fi

# Create orphan branch, replacing any existing new-main
# Use symbolic-ref to move HEAD without touching the working tree, then delete the
# old new-main (if any) so git checkout --orphan can recreate it cleanly.
git symbolic-ref HEAD refs/heads/new-main-tmp
git branch -D new-main 2>/dev/null || true
git checkout --orphan new-main
# Clear the index (orphan branch starts with everything staged from previous branch)
git rm -rf --cached . 2>/dev/null || true

# Write .gitignore before first commit (in the output repo)
printf '.fseventsd/\n.Spotlight-V100/\n' > "$JADX_REPO/.gitignore"


    # Extract APK into repo dir (covered by .gitignore)
    unzip -q -o "$ZIP" "$BASENAME" -d .

    # Wipe previous jadx output
    rm -rf "$JADX_REPO/SlamCamera/sources" "$JADX_REPO/SlamCamera/resources"
    mkdir -p "$JADX_REPO/SlamCamera"

    # Decompile
    docker run --rm \
        -v "$REPO_DIR:/work" \
        -v "/Volumes/CaseSensitive/InkJoy/com.inkjoyframe.domus:/work/InkJoy" \
        -v "$REPO_DIR/.framework:/work/framework:ro" \
        jadx sh -c "jadx --no-debug-info -d /work/InkJoy \"/work/$BASENAME\" /work/framework/system/framework/*.jar" || true

    # Clean up extracted APK
    rm -f "$BASENAME"

    # Normalise synthetic method names: strip the unstable counter prefix from
    # jadx-generated names so that diffs only show real code changes rather than
    # global counter shifts between versions.
    # Jadx synthetic name patterns, all with 4+ digit counters.
    # Known prefixes: m (synthetic), mo (method override). Capital M/Mo for
    # local variables derived from those names (jadx capitalises the prefix).
    #   m17024x6bcfd03e              → mx6bcfd03e        (unknown-name synthetics)
    #   mo18692trySendJP2dKIU        → motrySendJP2dKIU  (method overrides)
    #   m17011lambda$foo$0$Bar       → mlambda$foo$0$Bar (lambda synthetics)
    #   m17260constructorimpl        → mconstructorimpl  (Kotlin inline class)
    #   m17276ubyteArrayOfGBYM_sE   → mubyteArrayOfGBYM_sE (Kotlin mangled names)
    # Local variables get the same names capitalized after a type-prefix letter:
    #   jM17363constructorimpl       → jMconstructorimpl
    #   objMo18692trySendJP2dKIU    → objMotrySendJP2dKIU
    # The 4-digit minimum distinguishes jadx counters (always thousands+) from
    # real identifiers like m5Qi or m264Encoder.
    find "$JADX_REPO/InkJoy/sources" -name "*.java" \
        -exec sed -E -i '' \
            -e 's/m(o?)[0-9][0-9][0-9][0-9][0-9]*([A-Za-z][A-Za-z0-9_$]*)/m\1\2/g' \
            -e 's/([A-Za-z])M(o?)[0-9][0-9][0-9][0-9][0-9]*([A-Za-z][A-Za-z0-9_$]*)/\1M\2\3/g' \
            {} +

    # Stage and commit (scope to SlamCamera/ and .gitignore only so unrelated
    # files on the volume don't get swept in)
    git add -A -- SlamCamera/ .gitignore
    GIT_AUTHOR_DATE="$COMMIT_DATE" GIT_COMMITTER_DATE="$COMMIT_DATE" \
        git commit -m "Slam Camera $VERSION"

    GIT_COMMITTER_DATE="$COMMIT_DATE" \
        git tag "v$VERSION"

    echo "  Committed and tagged v$VERSION."
done

echo ""
echo "=== All done! ==="
echo ""
echo "New history is on branch 'new-main'. To replace main:"
echo ""
echo "  git branch -D main"
echo "  git branch -m new-main main"
echo ""
echo "Or to inspect first:  git log --oneline new-main"
