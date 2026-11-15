#!/bin/bash
# Imports the macOS signing certificate from the repository's secrets into
# a temporary keychain, for `mygo build -sign`, and names the identity in
# $GITHUB_ENV. A fixed certificate keeps the app's designated requirement
# from build to build, so macOS keeps its Accessibility permission across
# updates. Without the secret, as for pull requests from forks, it does
# nothing and the app is signed ad hoc.
set -euo pipefail

if [ -z "${MACOS_CERTIFICATE_P12:-}" ]; then
  echo "No signing certificate: the app is signed ad hoc."
  exit 0
fi

keychain="$RUNNER_TEMP/copied-signing.keychain-db"
p12="$RUNNER_TEMP/copied-signing.p12"
password=$(openssl rand -hex 16)
trap 'rm -f "$p12"' EXIT

printf '%s' "$MACOS_CERTIFICATE_P12" | base64 --decode > "$p12"
security create-keychain -p "$password" "$keychain"
security set-keychain-settings -lut 21600 "$keychain"
security unlock-keychain -p "$password" "$keychain"
security import "$p12" -k "$keychain" -P "$MACOS_CERTIFICATE_PASSWORD" -T /usr/bin/codesign
# Lets codesign use the key without a prompt.
security set-key-partition-list -S apple-tool:,apple: -s -k "$password" "$keychain" > /dev/null
# codesign looks for identities in the user's keychain search list.
# shellcheck disable=SC2046
security list-keychains -d user -s "$keychain" $(security list-keychains -d user | tr -d '"')

# The certificate is self-signed, so it is not trusted: look among all
# identities, not only the valid ones (-v).
identity=$(security find-identity -p codesigning "$keychain" | awk '/"Copied Code Signing"/ {print $2; exit}')
if [ -z "$identity" ]; then
  echo "The certificate holds no \"Copied Code Signing\" identity." >&2
  exit 1
fi
echo "MACOS_SIGNING_IDENTITY=$identity" >> "$GITHUB_ENV"
echo "MACOS_SIGNING_KEYCHAIN=$keychain" >> "$GITHUB_ENV"
echo "Signing with $identity"
