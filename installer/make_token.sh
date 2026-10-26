# shellcheck shell=bash
make_token() {
  if [ -z "$TOKEN" ]; then
    TOKEN="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    echo "   new token created"
  else
    echo "   existing token kept (not replaced)"
  fi
}
