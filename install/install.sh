#!/usr/bin/env bash
# BOBRES bootstrap installer. One command, as root:
#
#   bash <(curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh)
#
# or with sudo, optionally passing `bobres install` flags after `-s --`:
#
#   curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh | sudo bash
#   curl -fsSL .../install.sh | sudo bash -s -- --domain panel.example.com
#
# Afterwards, run `bobres` for the management menu.
#
# What this does: checks the OS, installs Docker if missing, downloads the `bobres` CLI,
# VERIFIES the release signature and checksum, installs it, then hands over to `bobres install`.
# It shows a banner and one progress bar from 0 to 100% that continues in `bobres install`
# (BOBRES_PROGRESS carries the share over); what Docker's and apt's own scripts print goes to
# /var/log/bobres-install.log, and a dropped SSH connection does not stop the install. Plain
# output (no colors, no redrawing) for pipes, logs and NO_COLOR: --plain, BOBRES_PLAIN=1.
#
# Trust model (read this): two vendor ROOT Ed25519 public keys are embedded below. They sign
# `keys.txt` (trusted release keys, with expiry, sequence number and revocations). A release
# key from that list signs `checksums.txt` (which carries version and expiry), and the binary
# must match the signed checksum. A compromised download host therefore
# cannot substitute a binary. The remaining trust roots are THIS script (fetched over HTTPS,
# so pin/verify it if you can) and Docker's official installer (https://get.docker.com).
#
# The whole script is wrapped in main() and only runs on the last line, so a truncated
# download can never execute a partial script.
set -euo pipefail

VERSION_SCRIPT="0.2.0"
BIN_NAME="bobres"
WORK=""

# ---- brand: the words of the banner. cmd/bobres/brand.go carries the same ones, and
# TestInstallShBrandMatchesGo fails when they differ.
BRAND_COPYRIGHT="© 2026 BOBRES, all rights reserved"
BRAND_MADE_BY="Made by TECSO-DEV"
BRAND_TAGLINE="Your VPN store on Telegram"
BRAND_LICENSE="Source available, all rights reserved. See LICENSE."
BRAND_SITE="https://sobhanaz.github.io/bobres-3x-ui-telegram-panel/"
BRAND_REPO="https://github.com/sobhanaz/bobres-3x-ui-telegram-panel"
BRAND_ISSUES="${BRAND_REPO}/issues"
BRAND_SECURITY="${BRAND_REPO}/security"

# ---- the steps of this script and their weights (percent of the whole install). `bobres install`
# has CLI_STEPS steps of its own that take the rest up to 100; TestInstallShStepsMatchGo fails
# when that count and its steps (cmd/bobres/installprogress.go) differ.
STEP_KEYS=(preflight docker download verify handoff)
STEP_NAMES=("Checking this system" "Installing Docker" "Downloading the bobres CLI" "Verifying signature and checksum" "Installing the bobres CLI")
STEP_WEIGHTS=(3 15 7 4 1)
CLI_STEPS=8

# ROOT public keys (Ed25519). Roots sign only keys.txt (the list of trusted release keys);
# a release key signs each release's checksums.txt. Any one root is enough to accept keys.txt,
# so losing one root is survivable. Rotate roots by shipping a new install.sh.
read -r -d '' RELEASE_ROOT_1 <<'PUBKEY' || true
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAKZqHG4ociEUeAtlxJX89TxKY7c3EEWS8JQHUKXBUKZw=
-----END PUBLIC KEY-----
PUBKEY
read -r -d '' RELEASE_ROOT_2 <<'PUBKEY' || true
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEADz7vJ+DMVvKQyotUaJSpnIOlvdbig/KAUzO49wfq3ng=
-----END PUBLIC KEY-----
PUBKEY
RELEASE_ROOTS=("$RELEASE_ROOT_1" "$RELEASE_ROOT_2")

# ---- terminal UI: the banner, the progress bar, the log and plain mode --------------------------
# Everything below is a function that only prints, so a test can source the script (see the last
# line) and call them. Output is plain (no escape bytes, no carriage returns, ASCII only) when
# stdout is not a terminal, NO_COLOR is set, TERM is dumb or unset, BOBRES_PLAIN=1 or --plain is
# given, or the terminal is under 60 columns wide. BOBRES_FORCE_TTY=1 makes stdout look like a
# terminal (tests only). The percent is honest: the weights of the finished steps plus the
# measured part of the running one; it never goes down and it is 100 only when everything is done.
# What programs print (get.docker.com, apt) goes to the log, never to the screen.
UI_MODE=plain      # plain or fancy
UI_UNICODE=0       # 1: block characters and a spinner of braille dots
UI_COLOR=0         # 0 none, 1 the 8 basic colors, 2 256 colors, 3 truecolor
UI_WIDTH=80
UI_FLAG_PLAIN=0    # --plain
UI_FLAG_NOBANNER=0 # --no-banner
UI_PROGRESS=0      # 1: show the steps
UI_STATE=()        # one of p (pending) r (running) d (done) s (skipped) per step
UI_CUR=-1          # the running step
UI_LAST=-1         # the step begun last
UI_FRAC=0          # how many percent of the running step are measured
UI_HIGH=0          # the highest percent shown: it never goes down
UI_PCT=0
UI_DETAIL=""
UI_FINISHED=0
UI_FAILED=0
UI_LIVE=0          # a step is running in a fancy terminal: the live line is the last line
UI_DRAWN=0         # the live line is on the cursor's line
UI_SPIN_PID=""
UI_RESUME=0
UI_PLAIN_LAST=""
UI_T0=0            # when the run began
UI_T_STEP=0        # when the running step began
UI_IDX=0
UI_LOG=/dev/null   # the install log, once it is open
UI_WARNS=0

ui_truthy() {
  case $(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]') in 1|true|yes|on) return 0 ;; esac
  return 1
}

# ui_utf8: is the locale UTF-8? The first of LC_ALL, LC_CTYPE and LANG that is set decides.
ui_utf8() {
  local v
  if [ -n "${LC_ALL:-}" ]; then v=$LC_ALL; elif [ -n "${LC_CTYPE:-}" ]; then v=$LC_CTYPE; else v=${LANG:-}; fi
  case $(printf '%s' "$v" | tr '[:upper:]' '[:lower:]') in *utf-8*|*utf8*) return 0 ;; esac
  return 1
}

# ui_width: the terminal's width in columns (80 when it cannot be told).
ui_width() {
  local w=""
  case ${COLUMNS:-} in ""|*[!0-9]*) ;; *) w=$COLUMNS ;; esac
  if [ -z "$w" ] && [ -t 1 ]; then
    w=$({ stty size </dev/tty; } 2>/dev/null | awk '{print $2}') || w=""
  fi
  case $w in ""|*[!0-9]*|0) w=80 ;; esac
  echo "$w"
}

# ui_init: decide plain or fancy and read the steps' states. Call it after the flags are parsed.
ui_init() {
  local tty=0 i
  UI_MODE=plain; UI_UNICODE=0; UI_COLOR=0
  UI_WIDTH=$(ui_width)
  if [ -t 1 ] || [ "${BOBRES_FORCE_TTY:-}" = "1" ]; then tty=1; fi
  if [ "$tty" -eq 1 ] && [ -z "${NO_COLOR:-}" ] && [ -n "${TERM:-}" ] && [ "${TERM:-}" != "dumb" ] \
     && ! ui_truthy "${BOBRES_PLAIN:-}" && [ "$UI_FLAG_PLAIN" -ne 1 ] && [ "$UI_WIDTH" -ge 60 ]; then
    UI_MODE=fancy
    if ui_utf8; then UI_UNICODE=1; fi
    case ${COLORTERM:-} in
      truecolor|24bit) UI_COLOR=3 ;;
      *) case ${TERM:-} in *256color*) UI_COLOR=2 ;; *) UI_COLOR=1 ;; esac ;;
    esac
  fi
  UI_STATE=(); i=0
  while [ "$i" -lt "${#STEP_KEYS[@]}" ]; do UI_STATE[i]=p; i=$((i + 1)); done
  UI_CUR=-1; UI_LAST=-1; UI_FRAC=0; UI_HIGH=0; UI_PCT=0; UI_FINISHED=0; UI_FAILED=0; UI_WARNS=0
  UI_LIVE=0; UI_DRAWN=0; UI_PLAIN_LAST=""; UI_T0=$(date +%s)
}

ui_banner_off() { [ "$UI_FLAG_NOBANNER" -eq 1 ] || ui_truthy "${BOBRES_NO_BANNER:-}"; }

# ui_log_open: open the install log (append, readable by root only). Nothing else of this script
# is ever written to it but what the steps print and the programs they run say.
ui_log_open() {
  local f=${BOBRES_INSTALL_LOG:-/var/log/bobres-install.log}
  if ( umask 077; : >>"$f" ) 2>/dev/null; then
    chmod 600 "$f" 2>/dev/null || true
    UI_LOG=$f
    printf '\n==== install.sh %s, %s ====\n' "$VERSION_SCRIPT" "$(date '+%Y-%m-%dT%H:%M:%S%z')" >>"$UI_LOG"
  else
    UI_LOG=/dev/null
  fi
}

# ui_log LINE: one line, with the time, to the log.
ui_log() { printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$1" >>"$UI_LOG" 2>/dev/null || true; }

# ui_survive: a dropped SSH connection (SIGHUP) no longer ends the install; what is printed from
# here on is also in the log, and printing to a closed terminal is not an error.
ui_survive() { trap '' HUP; }

# ui_sgr ROLE: the escape parameters of a palette color (navy, teal, brown, cream) or style.
ui_sgr() {
  case $UI_COLOR:$1 in
    3:navy) printf '38;2;59;91;140' ;;  3:teal) printf '38;2;20;184;166' ;;
    3:brown) printf '38;2;139;90;43' ;; 3:cream) printf '38;2;245;222;179' ;;
    2:navy) printf '38;5;60' ;;  2:teal) printf '38;5;37' ;;
    2:brown) printf '38;5;94' ;; 2:cream) printf '38;5;223' ;;
    1:navy) printf '34' ;;  1:teal) printf '36' ;;
    1:brown) printf '33' ;; 1:cream) printf '37' ;;
    *:bold) printf '1' ;; *:dim) printf '2' ;; *:red) printf '31' ;;
  esac
}

# ui_paint ROLE TEXT: TEXT in the color, or as it is where color is off.
ui_paint() {
  if [ "$UI_COLOR" -eq 0 ] || [ -z "$2" ]; then printf '%s' "$2"; return 0; fi
  printf '\033[%sm%s\033[0m' "$(ui_sgr "$1")" "$2"
}

# ui_mark KIND: the sign of an outcome (ok, warn, fail, skip): a check, a warning sign, a cross or
# a dash on a terminal, the words OK, WARN, FAIL and SKIP anywhere else.
ui_mark() {
  local k=$1 g
  if [ "$UI_MODE" != "fancy" ]; then
    case $k in ok) printf 'OK' ;; warn) printf 'WARN' ;; fail) printf 'FAIL' ;; *) printf 'SKIP' ;; esac
    return 0
  fi
  case $k in
    ok) if [ "$UI_UNICODE" -eq 1 ]; then g='✔'; else g='+'; fi; ui_paint teal "$g" ;;
    warn) ui_paint brown '!' ;;
    fail) if [ "$UI_UNICODE" -eq 1 ]; then g='✗'; else g='x'; fi; ui_paint red "$g" ;;
    *) ui_paint dim '-' ;;
  esac
}

# ui_pre / ui_post: around any line printed while a step runs, so it never lands on the live line.
ui_pre() {
  UI_RESUME=0
  if [ -n "$UI_SPIN_PID" ]; then ui_spin_stop; UI_RESUME=1; fi
  if [ "$UI_DRAWN" -eq 1 ]; then printf '\r\033[K' || true; UI_DRAWN=0; fi
}
ui_post() {
  if [ "$UI_LIVE" -eq 1 ]; then ui_draw ""; fi
  if [ "$UI_RESUME" -eq 1 ]; then ui_spin_start; fi
}

# info / warn / die: a line of the run. A closed terminal (a dropped SSH connection) is no error.
info() { ui_log "==> $*"; ui_pre; printf '%s %s\n' "$(ui_paint teal '==>')" "$*" || true; ui_post; }
warn() { UI_WARNS=$((UI_WARNS + 1)); ui_log "warning: $*"; ui_pre; printf '%s %s\n' "$(ui_paint brown 'warning:')" "$*" >&2 || true; ui_post; }
die()  {
  ui_log "error: $*"
  ui_abort
  printf '%s %s\n' "$(ui_paint red 'error:')" "$*" >&2 || true
  ui_fail_line
  ui_fail_help
  exit 1
}

# ---- the banner
# The beaver, then the BOBRES wordmark; cmd/bobres/brand.go draws the same (the banner test of
# the CLI compares them). O and # are cream, the rest brown.
ui_beaver_rows() {
  cat <<'ART'
   _          _
  (_)'------'(_)
  /    O  O    \
 |       __      |
 |      |##|     |   .------.
  \     |##|    /    |//////|
   '-.________.-'    '------'
ART
}
ui_wordmark_rows() {
  cat <<'ART'
####    ###   ####   ####   #####   ####
#   #  #   #  #   #  #   #  #      #
####   #   #  ####   ####   ####    ###
#   #  #   #  #   #  #  #   #          #
####    ###   ####   #   #  #####  ####
ART
}

# ui_beaver_paint ROW: one row of the beaver in color.
ui_beaver_paint() {
  local row=$1 i=0 c cur=brown run="" r
  if [ "$UI_COLOR" -eq 0 ]; then printf '%s' "$row"; return 0; fi
  while [ "$i" -lt "${#row}" ]; do
    c=${row:i:1}; r=brown
    case $c in O|'#') r=cream ;; ' ') r=$cur ;; esac
    if [ "$r" != "$cur" ]; then ui_paint "$cur" "$run"; run=""; cur=$r; fi
    run=$run$c; i=$((i + 1))
  done
  ui_paint "$cur" "$run"
}

# ui_banner [VERSION LINE]: the banner. The art needs 64 columns; under that, text only.
# Nothing in this script passes the optional argument (a caller may give its own version line),
# so shellcheck's SC2120 is silenced on purpose.
# shellcheck disable=SC2120
ui_banner() {
  local vline=${1:-"bobres installer ${VERSION_SCRIPT}"} copyright=$BRAND_COPYRIGHT
  local -a left=() right=()
  local row w=0 i label url
  [ "$UI_UNICODE" -eq 1 ] || copyright=${copyright//©/(c)}
  if [ "$UI_WIDTH" -ge 64 ]; then
    while IFS= read -r row; do left[${#left[@]}]=$row; [ "${#row}" -le "$w" ] || w=${#row}; done < <(ui_beaver_rows)
    right[0]=$vline
    while IFS= read -r row; do
      if [ "$UI_UNICODE" -eq 1 ]; then row=${row//#/█}; fi
      right[${#right[@]}]=$row
    done < <(ui_wordmark_rows)
    right[${#right[@]}]=$BRAND_TAGLINE
    i=0
    while [ "$i" -lt "${#left[@]}" ]; do
      row=${left[i]}
      while [ "${#row}" -lt $((w + 2)) ]; do row="$row "; done
      case $i in
        0) printf '%s%s\n' "$(ui_beaver_paint "$row")" "$(ui_paint bold "${right[i]}")" ;;
        6) printf '%s%s\n' "$(ui_beaver_paint "$row")" "$(ui_paint cream "${right[i]}")" ;;
        *) printf '%s%s\n' "$(ui_beaver_paint "$row")" "$(ui_paint teal "${right[i]}")" ;;
      esac
      i=$((i + 1))
    done
    printf '\n'
  else
    printf '%s\n%s\n' "$(ui_paint bold "$vline")" "$(ui_paint teal "$BRAND_TAGLINE")"
  fi
  printf '%s\n%s\n%s\n' "$(ui_paint cream "$copyright")" "$(ui_paint cream "$BRAND_MADE_BY")" "$BRAND_LICENSE"
  for label in "Website|$BRAND_SITE" "Docs|$BRAND_REPO" "Issues|$BRAND_ISSUES" "Security|$BRAND_SECURITY"; do
    url=${label#*|}; label=${label%%|*}
    printf '%s %s\n' "$(ui_paint teal "$(printf '%-8s' "$label")")" "$url"
  done
}

# ui_header TITLE: a section header: a title and a rule on a terminal, "== Title ==" anywhere else.
ui_header() {
  local n rule="-" line=""
  ui_log "== $1 =="
  if [ "$UI_MODE" != "fancy" ]; then printf '== %s ==\n' "$1" || true; return 0; fi
  if [ "$UI_UNICODE" -eq 1 ]; then rule="─"; fi
  n=$(( (UI_WIDTH > 61 ? 60 : UI_WIDTH - 1) - ${#1} - 1 ))
  while [ "$n" -gt 0 ]; do line="$line$rule"; n=$((n - 1)); done
  ui_pre
  printf '\n%s %s\n' "$(ui_paint bold "$(ui_paint teal "$1")")" "$(ui_paint navy "$line")" || true
  ui_post
}

# ---- the progress
ui_fmt_dur() {   # seconds -> 38s, 1m05s, 1h02m
  local s=$1
  if [ "$s" -lt 60 ]; then printf '%ds' "$s"
  elif [ "$s" -lt 3600 ]; then printf '%dm%02ds' $((s / 60)) $((s % 60))
  else printf '%dh%02dm' $((s / 3600)) $((s % 3600 / 60)); fi
}
ui_fmt_clock() { # seconds -> 0:38, 12:05, 1:02:03
  local s=$1
  if [ "$s" -ge 3600 ]; then printf '%d:%02d:%02d' $((s / 3600)) $((s % 3600 / 60)) $((s % 60))
  else printf '%d:%02d' $((s / 60)) $((s % 60)); fi
}

# ui_total: the number of steps in all, this script's and the CLI's.
ui_total() { echo $((${#STEP_KEYS[@]} + CLI_STEPS)); }
ui_tag()   { printf 'Step %d/%d' $(($1 + 1)) "$(ui_total)"; }
ui_title() { printf '%s: %s' "$(ui_tag "$1")" "${STEP_NAMES[$1]}"; }

# ui_calc: set UI_PCT to the percent now. It never goes down, and it is 99 at most until the
# run is finished (ui_finish).
ui_calc() {
  local i=0 sum=0 w
  for w in "${STEP_WEIGHTS[@]}"; do
    case ${UI_STATE[$i]} in
      d|s) sum=$((sum + w)) ;;
      r) sum=$((sum + w * UI_FRAC / 100)) ;;
    esac
    i=$((i + 1))
  done
  if [ "$sum" -ge 100 ] && [ "$UI_FINISHED" -ne 1 ]; then sum=99; fi
  if [ "$sum" -lt "$UI_HIGH" ]; then sum=$UI_HIGH; fi
  UI_HIGH=$sum; UI_PCT=$sum
}

# ui_plain TEXT: one plain line, "[ 42%] TEXT" (never twice in a row), also in the log.
ui_plain() {
  local line
  line=$(printf '[%3d%%] %s' "$UI_PCT" "$1")
  [ "$line" != "$UI_PLAIN_LAST" ] || return 0
  UI_PLAIN_LAST=$line
  ui_log "$line"
  printf '%s\n' "$line" || true
}

# ui_result I KIND LABEL DETAIL PAREN: the line of a finished, skipped, failed or reported step.
# Plain: "[ 42%] Step 4/13: OK label (detail)"; on a terminal: a mark, the label padded, the detail.
ui_result() {
  local i=$1 kind=$2 label=$3 detail=$4 paren=$5 text word col=38 d
  case $kind in ok) word=OK ;; warn) word=WARN ;; fail) word=FAIL ;; *) word=SKIP ;; esac
  text="$word $label"
  if [ -n "$detail" ]; then
    if [ "$paren" = "1" ]; then text="$text ($detail)"; else text="$text: $detail"; fi
  fi
  text="$(ui_tag "$i"): $text"
  if [ "$UI_MODE" != "fancy" ]; then ui_plain "$text"; return 0; fi
  ui_log "$(printf '[%3d%%] %s' "$UI_PCT" "$text")"
  d=$detail
  [ "$paren" != "1" ] || d=$(ui_paint dim "$detail")
  ui_pre
  printf '  %s %s  %s\n' "$(ui_mark "$kind")" "$(printf "%-${col}s" "$label")" "$d" || true
  ui_post
}

# ui_bar_line FRAME: "[bar] pct%  text  spinner elapsed" for the live line, never wider than the
# terminal less one column. FRAME is the spinner's frame number, or "" for no spinner.
ui_bar_line() {
  local bw=20 fill empty n i bar="" gap="" tail="" text room frame
  local -a spin=("|" "/" "-" "\\")
  [ "$UI_WIDTH" -ge 80 ] || bw=10
  fill="#"; empty="-"
  if [ "$UI_UNICODE" -eq 1 ]; then fill="█"; empty="░"; spin=("⠋" "⠙" "⠹" "⠸" "⠼" "⠴" "⠦" "⠧" "⠇" "⠏"); fi
  n=$((UI_PCT * bw / 100))
  i=0; while [ "$i" -lt "$n" ]; do bar="$bar$fill"; i=$((i + 1)); done
  i=0; while [ "$i" -lt $((bw - n)) ]; do gap="$gap$empty"; i=$((i + 1)); done
  if [ "$UI_FINISHED" -eq 1 ]; then text="Done"
  elif [ "$UI_CUR" -ge 0 ]; then text=$(ui_title "$UI_CUR"); [ -z "$UI_DETAIL" ] || text="$text  $UI_DETAIL"
  else text=""; fi
  if [ -n "$1" ]; then
    frame=${spin[$(($1 % ${#spin[@]}))]}
    tail="$frame $(ui_fmt_clock $(($(date +%s) - UI_T0)))"
  fi
  room=$((UI_WIDTH - 1 - (bw + 9) - ${#tail} - 2))
  if [ "$room" -lt 1 ]; then text=""; elif [ "${#text}" -gt "$room" ]; then text="${text:0:$((room - 3))}..."; fi
  printf '[%s%s] %s  %s' "$(ui_paint teal "$bar")" "$(ui_paint navy "$gap")" "$(ui_paint bold "$(printf '%3d%%' "$UI_PCT")")" "$text"
  if [ -n "$tail" ]; then printf '  %s' "$(ui_paint brown "$tail")"; fi
}

# ui_draw FRAME: draw the live line on the cursor's line.
ui_draw() {
  [ "$UI_MODE" = "fancy" ] || return 0
  ui_calc
  printf '\r\033[K%s' "$(ui_bar_line "$1")" || true
  UI_DRAWN=1
}

# ui_spin_start / ui_spin_stop: a spinner and the elapsed time on the live line while something
# runs that prints nothing. The cursor is hidden only meanwhile. The spinner is a background
# process that ends with this script (and is stopped by ui_abort on any way out).
ui_spin_start() {
  [ "$UI_MODE" = "fancy" ] && [ "$UI_LIVE" -eq 1 ] && [ -z "$UI_SPIN_PID" ] || return 0
  [ "${BOBRES_UI_NOSPIN:-}" != "1" ] || return 0
  printf '\033[?25l' || true
  (
    n=0
    trap 'exit 0' TERM
    while kill -0 "$$" 2>/dev/null; do
      ui_draw "$n" || exit 0
      n=$((n + 1))
      sleep 0.1
    done
  ) &
  UI_SPIN_PID=$!
}
ui_spin_stop() {
  [ -n "$UI_SPIN_PID" ] || return 0
  { kill "$UI_SPIN_PID"; wait "$UI_SPIN_PID"; } 2>/dev/null || true
  UI_SPIN_PID=""
  if [ "$UI_MODE" = "fancy" ]; then printf '\033[?25h' || true; fi
}

# ui_run COMMAND...: run a program whose output is not for the screen: it goes to the log, and a
# spinner shows meanwhile. Returns the program's status.
ui_run() {
  local rc=0
  ui_log "\$ $*"
  ui_spin_start
  "$@" >>"$UI_LOG" 2>&1 || rc=$?
  ui_spin_stop
  return "$rc"
}

# ui_step_index KEY: set UI_IDX to the step's index; fails for a step that is not listed.
ui_step_index() {
  local i=0 k
  for k in "${STEP_KEYS[@]}"; do
    if [ "$k" = "$1" ]; then UI_IDX=$i; return 0; fi
    i=$((i + 1))
  done
  return 1
}

# ui_step_begin KEY: the step starts (the one running ends first).
ui_step_begin() {
  [ "$UI_PROGRESS" -eq 1 ] || return 0
  ui_step_index "$1" || return 0
  ui_step_end
  UI_CUR=$UI_IDX; UI_LAST=$UI_IDX; UI_STATE[UI_CUR]=r; UI_FRAC=0; UI_DETAIL=""
  UI_T_STEP=$(date +%s)
  ui_calc
  if [ "$UI_MODE" = "fancy" ]; then
    ui_log "$(printf '[%3d%%] %s' "$UI_PCT" "$(ui_title "$UI_CUR")")"
    UI_LIVE=1; ui_draw ""
  else
    ui_plain "$(ui_title "$UI_CUR")"
  fi
}

# ui_step_frac DONE TOTAL DETAIL: DONE of TOTAL things of the running step are measured to be done.
ui_step_frac() {
  local f was
  [ "$UI_PROGRESS" -eq 1 ] && [ "$UI_CUR" -ge 0 ] && [ "$2" -gt 0 ] || return 0
  f=$((100 * $1 / $2))
  if [ "$f" -gt 100 ]; then f=100; fi
  # a measure below the best so far (a count that grew) changes nothing, not even the words
  [ "$f" -ge "$UI_FRAC" ] || return 0
  UI_FRAC=$f
  UI_DETAIL=$3
  ui_calc
  if [ "$UI_MODE" = "fancy" ]; then
    was=$UI_SPIN_PID
    ui_spin_stop
    ui_draw ""
    [ -z "$was" ] || ui_spin_start
  else
    ui_plain "$(ui_title "$UI_CUR") ($3)"
  fi
}

# ui_step_end: the running step is done.
ui_step_end() {
  local i=$UI_CUR took
  [ "$UI_PROGRESS" -eq 1 ] && [ "$i" -ge 0 ] || return 0
  ui_spin_stop
  took=$(ui_fmt_dur $(($(date +%s) - UI_T_STEP)))
  UI_STATE[i]=d; UI_CUR=-1; UI_LIVE=0
  ui_calc
  ui_result "$i" ok "${STEP_NAMES[$i]}" "$took" 1
}

# ui_step_skip KEY WHY: a step that does not apply counts as done.
ui_step_skip() {
  [ "$UI_PROGRESS" -eq 1 ] || return 0
  ui_step_index "$1" || return 0
  [ "${UI_STATE[UI_IDX]}" = "p" ] || return 0
  UI_STATE[UI_IDX]=s
  ui_calc
  ui_result "$UI_IDX" skip "${STEP_NAMES[$UI_IDX]}" "$2" 1
}

# ui_finish: every step is done and nothing else follows (a script that does not hand over).
ui_finish() {
  local i=0 extra=""
  [ "$UI_PROGRESS" -eq 1 ] || return 0
  ui_step_end
  while [ "$i" -lt "${#STEP_KEYS[@]}" ]; do
    [ "${UI_STATE[i]}" != "p" ] || UI_STATE[i]=s
    i=$((i + 1))
  done
  UI_FINISHED=1; ui_calc
  if [ "$UI_WARNS" -gt 0 ]; then extra=", $UI_WARNS warning(s)"; fi
  if [ "$UI_MODE" = "fancy" ]; then
    printf '%s  %s\n' "$(ui_bar_line "")" "$(ui_paint teal "in $(ui_fmt_dur $(($(date +%s) - UI_T0)))$extra")" || true
  else
    ui_plain "Done in $(ui_fmt_dur $(($(date +%s) - UI_T0)))$extra"
  fi
}

# ui_abort: put the terminal back: the spinner stops, the live line goes, the cursor shows.
ui_abort() {
  ui_spin_stop
  UI_LIVE=0
  if [ "$UI_DRAWN" -eq 1 ]; then printf '\r\033[K' || true; UI_DRAWN=0; fi
  if [ "$UI_MODE" = "fancy" ]; then printf '\033[?25h' || true; fi
}

# ui_fail_line: say where the run stopped (once); the percent stays where it was.
ui_fail_line() {
  local i=$UI_CUR
  [ "$UI_PROGRESS" -eq 1 ] && [ "$UI_FAILED" -eq 0 ] || return 0
  [ "$i" -ge 0 ] || i=$UI_LAST
  [ "$i" -ge 0 ] || return 0
  UI_FAILED=1; UI_CUR=-1
  ui_calc
  if [ "$UI_MODE" = "fancy" ]; then
    ui_result "$i" fail "${STEP_NAMES[$i]}" "failed at ${UI_PCT}%" 0
  else
    ui_result "$i" fail "${STEP_NAMES[$i]}" "" 1
  fi
}

# ui_fail_help: after a failure the end of the log and what to do.
ui_fail_help() {
  [ "$UI_PROGRESS" -eq 1 ] && [ "$UI_LOG" != "/dev/null" ] || return 0
  {
    printf '\nThe last lines of %s:\n' "$UI_LOG"
    tail -n 15 "$UI_LOG" 2>/dev/null | sed 's/^/  /'
    printf '\nWhat to do: fix the cause named above, then run the same command again: it is safe to repeat.\n'
  } >&2 || true
}

# ui_on_exit: the EXIT trap. A script that stops while a step runs says so.
ui_on_exit() {
  local rc=$?
  ui_abort
  if [ "$rc" -ne 0 ]; then ui_fail_line; fi
  rm -rf "${WORK:-}"
  return "$rc"
}

# semver_num N -> N if it is a number (digits only), else 0
semver_num() {
  case $1 in ""|*[!0-9]*) echo 0 ;; *) echo "$1" ;; esac
}

# semver_lt A B -> success if A < B, in semver 2.0 order (internal/release.Compare): X.Y.Z,
# then a pre-release sorts before its release (1.2.0-rc.1 < 1.2.0), and pre-releases compare
# identifier by identifier: numbers as numbers (rc.9 < rc.10) and before words, words in
# ASCII order, and more identifiers after fewer. A leading v and +build are ignored.
semver_lt() {
  local a=${1#v} b=${2#v} apre="" bpre="" a1 a2 a3 b1 b2 b3 x y
  a=${a%%+*}; b=${b%%+*}
  case $a in *-*) apre=${a#*-}; a=${a%%-*} ;; esac
  case $b in *-*) bpre=${b#*-}; b=${b%%-*} ;; esac
  IFS=. read -r a1 a2 a3 <<<"$a"
  IFS=. read -r b1 b2 b3 <<<"$b"
  a1=$(semver_num "${a1:-}"); a2=$(semver_num "${a2:-}"); a3=$(semver_num "${a3:-}")
  b1=$(semver_num "${b1:-}"); b2=$(semver_num "${b2:-}"); b3=$(semver_num "${b3:-}")
  [ "$a1" -lt "$b1" ] && return 0; [ "$a1" -gt "$b1" ] && return 1
  [ "$a2" -lt "$b2" ] && return 0; [ "$a2" -gt "$b2" ] && return 1
  [ "$a3" -lt "$b3" ] && return 0; [ "$a3" -gt "$b3" ] && return 1
  # the same X.Y.Z: a release is above its pre-releases
  [ "$apre" = "$bpre" ] && return 1
  [ -z "$apre" ] && return 1
  [ -z "$bpre" ] && return 0
  while :; do
    [ -z "$apre" ] && return 0   # A ran out first: fewer identifiers sort first
    [ -z "$bpre" ] && return 1
    x=${apre%%.*}; y=${bpre%%.*}
    case $apre in *.*) apre=${apre#*.} ;; *) apre="" ;; esac
    case $bpre in *.*) bpre=${bpre#*.} ;; *) bpre="" ;; esac
    [ "$x" = "$y" ] && continue
    case "$x" in
      ""|*[!0-9]*)
        case "$y" in ""|*[!0-9]*) [ "$x" \< "$y" ]; return ;; esac
        return 1 ;;                # a word sorts after a number
      *)
        case "$y" in ""|*[!0-9]*) return 0 ;; esac
        semver_numlt "$x" "$y"; return ;;
    esac
  done
}

# semver_numlt X Y -> success if the number X < Y (digits only, any length: compared without
# leading zeros by length, then digit by digit; the same number written differently, 01 and 1,
# by its text)
semver_numlt() {
  local x=${1#"${1%%[!0]*}"} y=${2#"${2%%[!0]*}"}
  x=${x:-0}; y=${y:-0}
  [ "${#x}" -lt "${#y}" ] && return 0; [ "${#x}" -gt "${#y}" ] && return 1
  [ "$x" = "$y" ] && { [ "$1" \< "$2" ]; return; }
  [ "$x" \< "$y" ]
}

# release_channel VALUE -> the update channel install.sh takes releases from (stable or beta,
# as internal/release.ParseChannel reads BOBRES_UPDATE_CHANNEL: empty is stable, any case);
# fails on anything else.
release_channel() {
  local c
  c=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')
  case $c in
    ""|stable) echo stable ;;
    beta) echo beta ;;
    *) return 1 ;;
  esac
}

# verify_release DIR STATE_DIR [CHANNEL]
# DIR holds checksums.txt, checksums.txt.sig, keys.txt, keys.txt.sig.N. Prints the verified
# version on success (last line "VERSION=x.y.z" is not used; use RELEASE_VERSION/KEYS_SEQ globals).
# On failure prints a reason to stderr and returns 1. Nothing is written to STATE_DIR here.
# CHANNEL (default stable) is the channel the install follows: stable refuses a release whose
# signed header says "channel beta" (internal/release.ChannelAllows), so a pre-release that ends
# up as GitHub's latest release is not installed unless BOBRES_UPDATE_CHANNEL=beta asks for it.
verify_release() {
  local dir=$1 state=$2 channel=${3:-stable} now root ok=0 n=0 f
  now=$(date -u +%Y-%m-%dT%H:%MZ)

  # 1. keys.txt must carry a valid signature from ANY embedded root
  for f in "$dir"/keys.txt.sig.*; do
    [ -e "$f" ] || continue
    for root in "${RELEASE_ROOTS[@]}"; do
      n=$((n + 1))
      printf '%s\n' "$root" > "$dir/root${n}.pem"
      if openssl pkeyutl -verify -pubin -inkey "$dir/root${n}.pem" -rawin \
           -in "$dir/keys.txt" -sigfile "$f" >/dev/null 2>&1; then ok=1; break 2; fi
    done
  done
  [ "$ok" -eq 1 ] || { echo "key list signature is INVALID (no embedded root verified it)" >&2; return 1; }

  # 2. key list freshness, anti-rollback
  [ "$(sed -n 1p "$dir/keys.txt")" = "bobres-keys v1" ] || { echo "unknown key list format" >&2; return 1; }
  local seq kexp minver
  seq=$(awk '$1=="seq"{print $2; exit}' "$dir/keys.txt")
  kexp=$(awk '$1=="expires"{print $2; exit}' "$dir/keys.txt")
  minver=$(awk '$1=="min_version"{print $2; exit}' "$dir/keys.txt")
  case $seq in ""|*[!0-9]*) echo "bad key list seq" >&2; return 1 ;; esac
  [ -n "$kexp" ] && [ -n "$minver" ] || { echo "key list is missing expires/min_version" >&2; return 1; }
  [ "$now" \< "$kexp" ] || { echo "key list expired at $kexp (check the system clock, or upgrade install.sh)" >&2; return 1; }
  if [ -f "$state/keys.seq" ]; then
    local last; last=$(cat "$state/keys.seq")
    case $last in ""|*[!0-9]*) last=0 ;; esac
    [ "$seq" -ge "$last" ] || { echo "key list seq $seq is older than the last seen $last (rollback attempt?)" >&2; return 1; }
  fi

  # 3. release header (inside signed bytes)
  local ver kid rexp
  ver=$(awk '$1=="#" && $2=="version"{print $3; exit}' "$dir/checksums.txt")
  kid=$(awk '$1=="#" && $2=="key_id"{print $3; exit}' "$dir/checksums.txt")
  rexp=$(awk '$1=="#" && $2=="expires"{print $3; exit}' "$dir/checksums.txt")
  [ -n "$ver" ] && [ -n "$kid" ] && [ -n "$rexp" ] || { echo "release header is missing version/key_id/expires" >&2; return 1; }
  if awk -v k="$kid" '$1=="revoked" && $2==k {f=1} END{exit !f}' "$dir/keys.txt"; then
    echo "release key $kid is REVOKED" >&2; return 1
  fi
  local b64
  b64=$(awk -v k="$kid" '$1=="key" && $2==k {print $3; exit}' "$dir/keys.txt")
  [ -n "$b64" ] || { echo "release key $kid is not in the signed key list" >&2; return 1; }
  { echo "-----BEGIN PUBLIC KEY-----"; printf '%s' "$b64" | fold -w 64; echo; echo "-----END PUBLIC KEY-----"; } > "$dir/rel.pem"
  openssl pkeyutl -verify -pubin -inkey "$dir/rel.pem" -rawin \
    -in "$dir/checksums.txt" -sigfile "$dir/checksums.txt.sig" >/dev/null 2>&1 \
    || { echo "release signature is INVALID; refusing to install (the download may have been tampered with)" >&2; return 1; }
  [ "$now" \< "$rexp" ] || { echo "release metadata expired at $rexp (stale or replayed release)" >&2; return 1; }
  if semver_lt "$ver" "$minver"; then echo "release $ver is below the minimum allowed $minver" >&2; return 1; fi
  local rchan
  rchan=$(awk '$1=="#" && $2=="channel"{print $3; exit}' "$dir/checksums.txt")
  if [ "$rchan" = "beta" ] && [ "$channel" != "beta" ]; then
    echo "release $ver is a beta release (a pre-release) and this install follows stable: to install it, run install.sh with BOBRES_UPDATE_CHANNEL=beta" >&2; return 1
  fi
  if [ -f "$state/release.version" ]; then
    local cur; cur=$(cat "$state/release.version")
    if [ -n "$cur" ] && semver_lt "$ver" "$cur"; then echo "release $ver is older than installed $cur (downgrade refused)" >&2; return 1; fi
  fi
  RELEASE_VERSION=$ver; KEYS_SEQ=$seq
  return 0
}

main() {
  export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
  umask 022

  # The latest (non-pre-release) GitHub release of this repository.
  local BASE_URL="${BOBRES_BASE_URL:-https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/latest/download}"
  # The channel followed: stable refuses a release signed for beta, wherever it is downloaded
  # from (even when GitHub serves it as the latest release).
  local CHANNEL
  local INSTALL_DIR="${BOBRES_BIN_DIR:-/usr/local/bin}"

  usage() {
    cat <<EOF
BOBRES installer ${VERSION_SCRIPT}

Usage: install.sh [installer flags] [-- bobres install flags]

Installer flags:
  -h, --help       Show this help
  -v, --version    Show the installer version
  --check          Only run system checks, install nothing
  --plain          Plain output: no colors, no redrawing (also BOBRES_PLAIN=1, NO_COLOR, a pipe)
  --no-banner      Do not show the banner (also BOBRES_NO_BANNER=1)

Everything else is passed to 'bobres install' (for example --domain, --ask-harden).
The license is installed after the store is up, never by this script:
  sudo bobres license activate        (a license key; it is asked for, out of 'ps' and history)
  sudo bobres license import < token  (a license token file)
or in the dashboard: System > License.

What Docker's and apt's own scripts print goes to /var/log/bobres-install.log. If your SSH
connection drops the install keeps going: follow it with  tail -f /var/log/bobres-install.log
(and connect with  ssh -o ServerAliveInterval=30 ...  next time).

Environment:
  BOBRES_BASE_URL  Where release files are downloaded from (must be https://)
  BOBRES_BIN_DIR   Where the bobres binary is installed (default /usr/local/bin)
  BOBRES_UPDATE_CHANNEL
                   stable (default) or beta. On stable a release signed for beta (a
                   pre-release) is refused; set beta, with BOBRES_BASE_URL pointing at
                   the pre-release, to install one.
  BOBRES_INSTALL_LOG
                   The install log (default /var/log/bobres-install.log, readable by root only)
EOF
  }

  local CHECK_ONLY=0
  local -a PASSTHROUGH=()
  while [ "$#" -gt 0 ]; do
    case "$1" in
      -h|--help) usage; return 0 ;;
      -v|--version) echo "install.sh ${VERSION_SCRIPT}"; return 0 ;;
      --check) CHECK_ONLY=1 ;;
      --plain) UI_FLAG_PLAIN=1; export BOBRES_PLAIN=1 ;;
      --no-banner) UI_FLAG_NOBANNER=1; export BOBRES_NO_BANNER=1 ;;
      # Nothing here installs a license (no flag, no environment variable): refuse the flag
      # rather than install an unlicensed store the operator thinks is licensed.
      --license|--license=*) die "install.sh does not take a license: after the install, run 'sudo bobres license activate' (or use System > License in the dashboard)" ;;
      --) shift; PASSTHROUGH+=("$@"); break ;;
      *) PASSTHROUGH+=("$1") ;;
    esac
    shift
  done

  ui_init
  if [ "$CHECK_ONLY" -ne 1 ]; then UI_PROGRESS=1; fi
  # One exit path that puts the terminal back (cursor, half-drawn line) and removes the work
  # directory: also on Ctrl-C, on an error and on a failing command under set -e.
  trap ui_on_exit EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  if ! ui_banner_off; then ui_banner; printf '\n'; fi

  CHANNEL="$(release_channel "${BOBRES_UPDATE_CHANNEL:-}")" \
    || die "BOBRES_UPDATE_CHANNEL must be stable or beta (got '${BOBRES_UPDATE_CHANNEL:-}')"

  # ---- transport: HTTPS only (tests may set BOBRES_ALLOW_INSECURE_HTTP=1) ----
  case "$BASE_URL" in
    https://*) ;;
    http://*) [ "${BOBRES_ALLOW_INSECURE_HTTP:-0}" = "1" ] || die "BOBRES_BASE_URL must start with https://" ;;
    *) die "BOBRES_BASE_URL must start with https://" ;;
  esac
  local -a CURL=(curl -fsSL --retry 3 --connect-timeout 15 --max-time 300)
  if [ "${BOBRES_ALLOW_INSECURE_HTTP:-0}" != "1" ]; then
    CURL+=(--proto '=https' --tlsv1.2)
  fi

  # ---- root ----
  [ "$(id -u)" -eq 0 ] || die "this installer must run as root. Try: curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh | sudo bash"
  if [ "$CHECK_ONLY" -ne 1 ]; then
    ui_log_open
    ui_survive
  fi

  ui_header "Prepare"
  ui_step_begin preflight

  # ---- platform ----
  [ "$(uname -s)" = "Linux" ] || die "only Linux is supported (Ubuntu 22.04/24.04, Debian 11/12/13)"
  [ -r /etc/os-release ] || die "cannot read /etc/os-release; unsupported system"
  local OS_ID OS_VER ARCH
  # shellcheck disable=SC1091
  OS_ID="$(. /etc/os-release && echo "${ID:-unknown}")"
  # shellcheck disable=SC1091
  OS_VER="$(. /etc/os-release && echo "${VERSION_ID:-unknown}")"
  case "${OS_ID}:${OS_VER}" in
    ubuntu:22.04|ubuntu:24.04|debian:11|debian:12|debian:13) ;;
    *) die "unsupported OS ${OS_ID} ${OS_VER}. Supported: Ubuntu 22.04/24.04, Debian 11/12/13" ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) die "unsupported CPU architecture $(uname -m) (need amd64 or arm64)" ;;
  esac
  info "System: ${OS_ID} ${OS_VER} (${ARCH})"

  # ---- prerequisites (openssl verifies the release signature) ----
  local tool
  for tool in curl sha256sum awk; do
    command -v "$tool" >/dev/null 2>&1 || die "'$tool' is required but not installed (apt-get install -y curl coreutils gawk)"
  done
  if ! command -v openssl >/dev/null 2>&1; then
    if [ "$CHECK_ONLY" -eq 1 ]; then
      warn "openssl is missing; the real install will install it"
    else
      info "Installing openssl (needed to verify the release signature)"
      export DEBIAN_FRONTEND=noninteractive
      ui_run apt-get update -qq </dev/null || true
      ui_run apt-get install -y -qq openssl </dev/null || die "could not install openssl"
    fi
  fi

  if [ "$CHECK_ONLY" -eq 1 ]; then
    info "Checks passed. Nothing installed (--check)."
    return 0
  fi

  # ---- docker (children get </dev/null so they cannot eat the rest of a piped script) ----
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    info "Docker already installed"
    ui_step_end
    ui_step_skip docker "already installed"
  else
    ui_step_begin docker
    info "Installing Docker (official convenience script from https://get.docker.com; its output is in $UI_LOG)"
    local tmp_docker
    tmp_docker="$(mktemp)"
    ui_run "${CURL[@]}" https://get.docker.com -o "$tmp_docker" || die "could not download the Docker installer from https://get.docker.com"
    ui_run sh "$tmp_docker" </dev/null || die "the Docker installer failed"
    rm -f "$tmp_docker"
    ui_run systemctl enable --now docker </dev/null || true
    docker info >/dev/null 2>&1 || die "Docker was installed but the daemon is not reachable"
  fi
  docker compose version >/dev/null 2>&1 || die "Docker Compose v2 plugin is missing (apt-get install -y docker-compose-plugin)"

  # ---- download ----
  local ASSET
  WORK="$(mktemp -d)"   # global on purpose: the EXIT trap runs after main() has returned
  ASSET="${BIN_NAME}_linux_${ARCH}"

  ui_step_begin download
  info "Downloading release files"
  ui_spin_start
  "${CURL[@]}" -o "${WORK}/${ASSET}" "${BASE_URL}/${ASSET}" || die "download failed: ${BASE_URL}/${ASSET}"
  ui_step_frac 1 4 "1/4 files"
  "${CURL[@]}" -o "${WORK}/checksums.txt" "${BASE_URL}/checksums.txt" || die "download failed: ${BASE_URL}/checksums.txt"
  ui_step_frac 2 4 "2/4 files"
  "${CURL[@]}" -o "${WORK}/checksums.txt.sig" "${BASE_URL}/checksums.txt.sig" || die "download failed: ${BASE_URL}/checksums.txt.sig (releases must be signed)"
  ui_step_frac 3 4 "3/4 files"
  "${CURL[@]}" -o "${WORK}/keys.txt" "${BASE_URL}/keys.txt" || die "download failed: ${BASE_URL}/keys.txt"
  ui_step_frac 4 4 "4/4 files"
  local n
  for n in 1 2 3 4; do
    "${CURL[@]}" -o "${WORK}/keys.txt.sig.${n}" "${BASE_URL}/keys.txt.sig.${n}" 2>/dev/null || rm -f "${WORK}/keys.txt.sig.${n}"
  done

  ui_step_begin verify
  info "Verifying release signature"
  local STATE_DIR="${BOBRES_STATE_DIR:-/var/lib/bobres}"
  verify_release "$WORK" "$STATE_DIR" "$CHANNEL" || die "verification failed; refusing to install"

  info "Verifying checksum"
  local EXPECTED ACTUAL
  EXPECTED="$(awk -v a="$ASSET" '$2 == a || $2 == "*" a {print $1; exit}' "${WORK}/checksums.txt")"
  [ -n "$EXPECTED" ] || die "no checksum listed for ${ASSET}; refusing to install"
  ACTUAL="$(sha256sum "${WORK}/${ASSET}" | awk '{print $1}')"
  [ "$EXPECTED" = "$ACTUAL" ] || die "checksum mismatch for ${ASSET} (expected ${EXPECTED}, got ${ACTUAL}); aborting"

  # ---- install atomically: copy next to the target, then rename ----
  ui_step_begin handoff
  install -d -m 0755 "$INSTALL_DIR"
  install -m 0755 "${WORK}/${ASSET}" "${INSTALL_DIR}/.${BIN_NAME}.new"
  mv -f "${INSTALL_DIR}/.${BIN_NAME}.new" "${INSTALL_DIR}/${BIN_NAME}"
  install -d -m 0755 "$STATE_DIR"
  printf '%s\n' "$KEYS_SEQ" > "$STATE_DIR/keys.seq"
  printf '%s\n' "$RELEASE_VERSION" > "$STATE_DIR/release.version"
  # The verified manifest, so that `bobres check` can say when its signature expires.
  install -m 0644 "${WORK}/checksums.txt" "$STATE_DIR/checksums.txt"
  info "Installed ${INSTALL_DIR}/${BIN_NAME} ($("${INSTALL_DIR}/${BIN_NAME}" version </dev/null))"
  ui_step_end

  info "Starting $(ui_paint bold "bobres install")"
  # The CLI continues this script's bar: the percent done, the share left, the steps done and
  # the steps in all. It also appends to the same log, and is plain where this script is.
  BOBRES_PROGRESS="${UI_HIGH}:$((100 - UI_HIGH)):${#STEP_KEYS[@]}:$(ui_total)"
  export BOBRES_PROGRESS
  if [ "$UI_LOG" != "/dev/null" ]; then export BOBRES_INSTALL_LOG="$UI_LOG"; fi
  if [ "$UI_MODE" = "plain" ]; then export BOBRES_PLAIN=1; fi
  # exec replaces this shell, so what the EXIT trap does is done here first.
  ui_abort
  rm -rf "${WORK:-}"
  WORK=""
  trap - EXIT
  # Interactive prompts need a real terminal, not the (already consumed) pipe. /dev/tty can
  # exist yet be unopenable (no controlling terminal: CI, cron, docker without -t), so test
  # by opening it, and fall back to /dev/null (non-interactive flags are then required).
  if { : </dev/tty; } 2>/dev/null; then
    exec "${INSTALL_DIR}/${BIN_NAME}" install ${PASSTHROUGH[@]+"${PASSTHROUGH[@]}"} </dev/tty
  fi
  exec "${INSTALL_DIR}/${BIN_NAME}" install ${PASSTHROUGH[@]+"${PASSTHROUGH[@]}"} </dev/null
}

# Runs only if the whole file was downloaded (this is the last line).
[ "${BOBRES_INSTALL_SOURCE_ONLY:-0}" = "1" ] || main "$@"
