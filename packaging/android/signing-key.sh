#!/usr/bin/env bash
# Provides the signing key for Android release builds (used by the release
# workflow; writes HD_KEYSTORE* variables to $GITHUB_ENV). Android installs an
# update only if it is signed with the same key as the installed app.
#
#  1. HD_KEYSTORE_BASE64 (+ HD_KEYSTORE_PASSWORD, HD_KEY_ALIAS, HD_KEY_PASSWORD):
#     an own keystore, see docs/releases.md.
#  2. HD_SIGNING_PASSWORD only: the key is kept encrypted with this password in
#     android/signing/release.p12.enc. On the first run it is created; with
#     COMMIT_BRANCH set it is committed to that branch.
#  3. Nothing: a temporary debug key (updates do not install over older versions).
set -euo pipefail
cd "$(dirname "$0")/../.."

out="${RUNNER_TEMP:-/tmp}/homelydrive-release.p12"
enc=android/signing/release.p12.enc
env_file="${GITHUB_ENV:-/dev/stdout}"

if [ -n "${HD_KEYSTORE_BASE64:-}" ]; then
  echo "$HD_KEYSTORE_BASE64" | base64 -d > "$out"
  {
    echo "HD_KEYSTORE=$out"
    echo "HD_KEYSTORE_PASSWORD=${HD_KEYSTORE_PASSWORD:-}"
    echo "HD_KEY_ALIAS=${HD_KEY_ALIAS:-}"
    echo "HD_KEY_PASSWORD=${HD_KEY_PASSWORD:-}"
  } >> "$env_file"
  echo "Signing with the keystore from HD_KEYSTORE_BASE64."
elif [ -n "${HD_SIGNING_PASSWORD:-}" ]; then
  if [ -f "$enc" ]; then
    openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -in "$enc" -out "$out" -pass env:HD_SIGNING_PASSWORD
    echo "Signing with the key from $enc."
  else
    keytool -genkeypair -keystore "$out" -storetype PKCS12 -alias homelydrive \
      -keyalg RSA -keysize 4096 -validity 36500 -dname "CN=Homelydrive, O=Homelydrive" \
      -storepass:env HD_SIGNING_PASSWORD -keypass:env HD_SIGNING_PASSWORD
    mkdir -p "$(dirname "$enc")"
    openssl enc -aes-256-cbc -pbkdf2 -iter 600000 -salt -in "$out" -out "$enc" -pass env:HD_SIGNING_PASSWORD
    echo "Created a new signing key in $enc."
    if [ -n "${COMMIT_BRANCH:-}" ]; then
      git config user.name "github-actions[bot]"
      git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
      git add "$enc"
      git commit -m "Android: verschlüsselter Signierschlüssel für Updates"
      git push origin "HEAD:refs/heads/$COMMIT_BRANCH"
    else
      echo "::warning::New signing key created but not committed - commit $enc or the next build makes another key."
    fi
  fi
  {
    echo "HD_KEYSTORE=$out"
    echo "HD_KEYSTORE_PASSWORD=$HD_SIGNING_PASSWORD"
    echo "HD_KEY_ALIAS=homelydrive"
    echo "HD_KEY_PASSWORD=$HD_SIGNING_PASSWORD"
  } >> "$env_file"
else
  echo "::warning::No signing key (secret HD_SIGNING_PASSWORD, see docs/releases.md) - the APK is signed with a temporary key and updates will not install over an older version."
fi
