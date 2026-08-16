#!/bin/bash
# SessionStart hook — Claude Code on the web.
#
# Mendaftarkan Neon MCP server (dev/testing DB untuk task 5.1, lihat CATATAN_KEPUTUSAN.md)
# supaya sesi punya akses tool ke Neon (query/inspect DB dev) tanpa OAuth interaktif.
#
# Kunci API TIDAK PERNAH masuk repo (NFR-3, .env/.env.example rule): dibaca dari env var
# NEON_API_KEY yang harus diset lewat pengaturan environment Claude Code on the web
# (Settings environment → Environment variables), BUKAN dari file yang di-commit.
# Registrasi dipakai scope "local" (claude mcp add default) — tersimpan di ~/.claude.json
# milik kontainer sesi ini saja, bukan di repo.
#
# Hanya jalan di sesi remote (web); tidak menyentuh setup lokal developer.
set -euo pipefail

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

if [ -z "${NEON_API_KEY:-}" ]; then
  echo "NEON_API_KEY tidak diset — lewati setup Neon MCP server (lihat .claude/hooks/session-start.sh)." >&2
  exit 0
fi

if claude mcp list 2>/dev/null | grep -q '^neon:'; then
  exit 0
fi

claude mcp add neon -- npx -y @neondatabase/mcp-server-neon start "$NEON_API_KEY"
