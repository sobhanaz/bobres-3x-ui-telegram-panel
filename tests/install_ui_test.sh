#!/usr/bin/env bash
# Tests the UI helpers of install/install.sh (banner, progress bar, plain mode, log, cursor): a
# driver script sources them, runs a function with stdout in a file, and the file is checked.
# No Docker, no network. bash 3.2 + Linux.
# shellcheck disable=SC2034,SC2015
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
T="$(mktemp -d 2>/dev/null || mktemp -d -t bobres-ui)"
trap 'rm -rf "$T"' EXIT
fails=0
ok()  { echo "PASS: $1"; }
bad() { echo "FAIL: $1"; fails=$((fails + 1)); }
check() { if "$@"; then ok "$DESC"; else bad "$DESC"; fi; }

ESC=$(printf '\033')
CR=$(printf '\r')
# pure_ascii FILE: no byte outside printable ASCII, tab and newline
pure_ascii() { ! LC_ALL=C grep -q '[^[:print:][:space:]]' "$1"; }
# widest FILE: the widest line, in columns, with the escape sequences taken out
# (a redraw starts at a carriage return; every glyph the UI uses is one column)
widest() {
  tr '\r' '\n' <"$1" | sed -e "s/$ESC\\[[0-9;?]*[A-Za-z]//g" -e 's/█/X/g' -e 's/░/X/g' -e 's/✔/X/g' -e 's/✗/X/g' -e 's/─/X/g' -e 's/©/X/g' |
    awk '{ if (length($0) > m) m = length($0) } END { print m + 0 }'
}
no_esc() { ! grep -q "$ESC" "$1"; }
no_cr()  { ! grep -q "$CR" "$1"; }

# ---- the scenarios, as functions of the driver
# ten_steps: a faked 10-step run (the weights add up to 100).
ten_steps() {
  STEP_KEYS=(s1 s2 s3 s4 s5 s6 s7 s8 s9 s10)
  STEP_NAMES=("Checking this system" "Installing Docker" "Downloading the CLI" "Verifying" "Writing files" "Pulling images" "Starting the stack" "Waiting for health" "Hardening" "Summary")
  STEP_WEIGHTS=(5 5 10 10 5 30 10 15 5 5)
  CLI_STEPS=0
  ui_init
  UI_PROGRESS=1
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    ui_step_begin "s$i"
    case $i in
      3) ui_step_frac 1 4 "1/4 files"; ui_step_frac 4 4 "4/4 files" ;;
      6) ui_step_frac 1 6 "1/6 images"; ui_step_frac 3 6 "3/6 images"; ui_step_frac 2 6 "2/6 images"; ui_step_frac 6 6 "6/6 images" ;;
      8) ui_step_frac 2 5 "2/5 healthy"; ui_step_frac 5 5 "5/5 healthy" ;;
    esac
  done
  ui_finish
}
banner() { ui_init; ui_banner; }
# handoff: this script's own steps run, then the value it hands to bobres install.
handoff() {
  ui_init; UI_PROGRESS=1
  local k
  for k in "${STEP_KEYS[@]}"; do ui_step_begin "$k"; done
  ui_step_end
  echo "BOBRES_PROGRESS=${UI_HIGH}:$((100 - UI_HIGH)):${#STEP_KEYS[@]}:$(ui_total)"
}
# failure: a run that stops in its second step.
failure() {
  STEP_KEYS=(a b c); STEP_NAMES=(One Two Three); STEP_WEIGHTS=(20 30 50); CLI_STEPS=0
  ui_init; UI_PROGRESS=1
  ui_step_begin a; ui_step_begin b; ui_step_frac 1 2 "1/2"
  ui_fail_line
  ui_fail_line
}
# logging: a program's output goes to the log and not to the screen.
logging() {
  BOBRES_INSTALL_LOG="$LOGFILE"
  ui_log_open
  ui_init
  ui_run sh -c 'echo noisy-docker-output; echo noisy-apt-error >&2'
  echo "status=$?"
  ui_run sh -c 'exit 3'
  echo "status=$?"
}
# spinning WAY: a step with the spinner running, left in the way WAY says.
spinning() {
  STEP_KEYS=(a); STEP_NAMES=(One); STEP_WEIGHTS=(100); CLI_STEPS=0
  trap ui_on_exit EXIT
  ui_init; UI_PROGRESS=1
  ui_step_begin a
  ui_spin_start
  sleep 0.4
  case $1 in abort) ui_abort ;; end) ui_step_end ;; fail) exit 1 ;; esac
}
hup_trap() { ui_survive; trap -p HUP; }

# The driver: sources install.sh (it runs nothing by itself), defines the scenarios, runs "$@".
{
  echo 'BOBRES_INSTALL_SOURCE_ONLY=1'
  echo ". \"$ROOT/install/install.sh\""
  echo 'set +e'
  declare -f ten_steps banner handoff failure logging spinning hup_trap
  echo '"$@"'
} >"$T/drv.sh"

# Terminal looks: plain (no tty), fancy (forced tty, 256 colors, UTF-8, no spinner process).
base() { env -u NO_COLOR -u BOBRES_PLAIN -u BOBRES_FORCE_TTY -u BOBRES_UI_NOSPIN LOGFILE="$T/install.log" "$@"; }
plain() { base TERM=xterm-256color LC_ALL=en_US.UTF-8 bash "$T/drv.sh" "$@"; }
fancy() { base BOBRES_FORCE_TTY=1 BOBRES_UI_NOSPIN=1 TERM=xterm-256color COLUMNS="${COLS:-100}" LC_ALL="${LOC:-en_US.UTF-8}" bash "$T/drv.sh" "$@"; }

# ---- plain mode: a faked 10-step run
plain ten_steps >"$T/plain.txt" 2>&1
DESC="plain run: no escape bytes";            check no_esc "$T/plain.txt"
DESC="plain run: no carriage returns";        check no_cr "$T/plain.txt"
DESC="plain run: pure ASCII";                 check pure_ascii "$T/plain.txt"
DESC="plain run: every line is [ nn%] ...";   check bash -c "! grep -v -E '^\[ *[0-9]+%\] ' '$T/plain.txt'"
DESC="plain run: a begin line and an OK line with the time"; check bash -c "grep -q '^\[  0%\] Step 1/10: Checking this system' '$T/plain.txt' && grep -q 'Step 1/10: OK Checking this system (' '$T/plain.txt'"
DESC="plain run: fractions show";             check grep -q 'Step 6/10: Pulling images (3/6 images)' "$T/plain.txt"
DESC="plain run: a lower fraction is ignored"; check bash -c "! grep -q '2/6 images' '$T/plain.txt'"
pcts=$(sed -n 's/^\[ *\([0-9]*\)%\].*/\1/p' "$T/plain.txt")
prev=-1; mono=1
for p in $pcts; do [ "$p" -ge "$prev" ] || mono=0; prev=$p; done
DESC="plain run: the percent never goes down"; check test "$mono" -eq 1
DESC="plain run: ends at exactly 100%";        check test "$prev" -eq 100
DESC="plain run: 100% only on the last line";  check bash -c "[ \$(grep -c '^\[100%\]' '$T/plain.txt') -eq 1 ] && tail -n 1 '$T/plain.txt' | grep -q '^\[100%\] Done in '"

# ---- fancy mode: a faked run on a forced terminal
fancy ten_steps >"$T/fancy.txt" 2>&1
DESC="fancy run: redraws with clear-to-end";   check grep -q "$ESC\\[K" "$T/fancy.txt"
DESC="fancy run: draws the bar";               check bash -c "grep -q '█' '$T/fancy.txt' && grep -q '░' '$T/fancy.txt'"
DESC="fancy run: marks finished steps with a check"; check grep -q '✔' "$T/fancy.txt"
DESC="fancy run: ends with 100% and the time"; check bash -c "tail -n 1 '$T/fancy.txt' | grep -q '100%.*in '"
DESC="fancy run: lines fit the 100-column terminal"; check test "$(widest "$T/fancy.txt")" -lt 100
COLS=70 fancy ten_steps >"$T/narrow.txt" 2>&1
DESC="narrow terminal (70): the bar never wraps"; check test "$(widest "$T/narrow.txt")" -lt 70

# ---- what makes it plain
for v in "NO_COLOR=1" "BOBRES_PLAIN=1" "TERM=dumb" "TERM="; do
  base BOBRES_FORCE_TTY=1 COLUMNS=100 LC_ALL=en_US.UTF-8 TERM=xterm-256color "$v" bash "$T/drv.sh" ten_steps >"$T/p.txt" 2>&1
  DESC="plain with $v even on a terminal"; check bash -c "! grep -q '$ESC' '$T/p.txt' && ! grep -q '$CR' '$T/p.txt' && grep -q '^\[100%\]' '$T/p.txt'"
done
COLS=50 fancy ten_steps >"$T/p50.txt" 2>&1
DESC="plain under 60 columns";                 check bash -c "! grep -q '$ESC' '$T/p50.txt' && ! grep -q '$CR' '$T/p50.txt'"

# ---- the banner
plain banner >"$T/b_plain.txt" 2>&1
fancy banner >"$T/b_fancy.txt" 2>&1
LOC=C fancy banner >"$T/b_c.txt" 2>&1
COLS=62 fancy banner >"$T/b_narrow.txt" 2>&1
DESC="banner (plain) fits 78 columns";         check test "$(widest "$T/b_plain.txt")" -le 78
DESC="banner (fancy) fits 78 columns";         check test "$(widest "$T/b_fancy.txt")" -le 78
DESC="banner (plain): pure ASCII, no escapes, no CR"; check bash -c "! LC_ALL=C grep -q '[^[:print:][:space:]]' '$T/b_plain.txt' && ! grep -q '$ESC' '$T/b_plain.txt' && ! grep -q '$CR' '$T/b_plain.txt'"
DESC="banner (fancy, locale C): pure ASCII after the colors are taken out"; check bash -c "! sed 's/$ESC\\[[0-9;]*m//g' '$T/b_c.txt' | LC_ALL=C grep -q '[^[:print:][:space:]]'"
DESC="banner (fancy, UTF-8): block letters and the copyright sign"; check bash -c "grep -q '█' '$T/b_fancy.txt' && grep -q '©' '$T/b_fancy.txt'"
DESC="banner (plain) says (c) 2026 BOBRES, all rights reserved"; check grep -q '^(c) 2026 BOBRES, all rights reserved$' "$T/b_plain.txt"
DESC="banner has the credit line right under the copyright"; check bash -c "grep -A1 '^(c) 2026 BOBRES, all rights reserved$' '$T/b_plain.txt' | tail -n 1 | grep -q '^Made by TECSO-DEV$'"
DESC="banner has the tagline, the license and the links"; check bash -c "grep -q 'Your VPN store on Telegram' '$T/b_plain.txt' && grep -q 'Source available, all rights reserved. See LICENSE.' '$T/b_plain.txt' && grep -q 'https://sobhanaz.github.io/bobres-3x-ui-telegram-panel/' '$T/b_plain.txt' && grep -q '/issues' '$T/b_plain.txt' && grep -q '/security' '$T/b_plain.txt'"
DESC="banner (62 columns): text only";         check bash -c "! grep -q '####' '$T/b_narrow.txt'"
DESC="banner is at most 15 lines";             check test "$(wc -l <"$T/b_plain.txt")" -le 15

# ---- the hand-over: this script's steps end at their weights, the CLI takes the rest
plain handoff >"$T/handoff.txt" 2>&1
DESC="hand-over: BOBRES_PROGRESS is 30:70:5:13"; check grep -q '^BOBRES_PROGRESS=30:70:5:13$' "$T/handoff.txt"

# ---- a failure keeps the percent and says where it stopped (once)
plain failure >"$T/fail.txt" 2>&1
DESC="failure: one FAIL line, at the percent it had"; check bash -c "[ \$(grep -c 'FAIL' '$T/fail.txt') -eq 1 ] && tail -n 1 '$T/fail.txt' | grep -q '^\[ 35%\] Step 2/3: FAIL Two'"

# ---- the log: a program's output goes there and not to the screen; mode 600
plain logging >"$T/log.txt" 2>&1
DESC="log: the program's output is in the log"; check grep -q 'noisy-docker-output' "$T/install.log"
DESC="log: and not on the screen";             check bash -c "! grep -q 'noisy' '$T/log.txt'"
DESC="log: ui_run returns the program's status"; check bash -c "grep -q '^status=0$' '$T/log.txt' && grep -q '^status=3$' '$T/log.txt'"
mode=$(stat -c %a "$T/install.log" 2>/dev/null || stat -f %Lp "$T/install.log")
DESC="log: mode 600";                          check test "$mode" = "600"

# ---- the cursor: hidden only while the spinner runs, shown again on every way out
for way in abort end fail; do
  base BOBRES_FORCE_TTY=1 TERM=xterm-256color COLUMNS=100 LC_ALL=en_US.UTF-8 bash "$T/drv.sh" spinning "$way" >"$T/spin.txt" 2>&1
  hides=$(grep -c "$ESC\\[?25l" "$T/spin.txt")
  last=$(grep -o "$ESC\\[?25[lh]" "$T/spin.txt" | tail -n 1)
  DESC="spinner ($way): the cursor was hidden, and the last cursor command is show"; check bash -c "[ $hides -ge 1 ] && [ \"$last\" = \"${ESC}[?25h\" ]"
done

# ---- a dropped connection does not end the install
plain hup_trap >"$T/hup.txt" 2>&1
DESC="ui_survive ignores SIGHUP";              check grep -q "trap -- '' SIGHUP" "$T/hup.txt"

echo
if [ "$fails" -eq 0 ]; then echo "all install UI tests passed"; exit 0; fi
echo "$fails install UI test(s) FAILED"; exit 1
