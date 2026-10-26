# shellcheck shell=bash
step_config() {
  say "2/6 Configuration and token"
  load_config
  pick_port
  make_token
  write_config
  make_log_dir
  make_trash_dir
  make_notes_dir
}
