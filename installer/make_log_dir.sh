# shellcheck shell=bash
make_log_dir() {
  mkdir -p "$LOGDIR"
  chmod 700 "$LOGDIR"
  echo "   $LOGDIR (mode 700, logs)"
}
