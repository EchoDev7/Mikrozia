#!/bin/sh

# Set home directory explicitly
export HOME="${HOME:-/root}"

# Persistent ACME state management & non-destructive migration
# Ensures acme.sh accounts, configs, keys, and renewal state persist in /root/cert/.acme.sh
ACME_CERT_DIR="${ACME_CERT_DIR:-/root/cert}"
ACME_PERSIST_DIR="${ACME_PERSIST_DIR:-$ACME_CERT_DIR/.acme.sh}"
ACME_ROOT_DIR="${ACME_ROOT_DIR:-/root/.acme.sh}"
ACME_SLASH_DIR="${ACME_SLASH_DIR:-/.acme.sh}"

mkdir -p "$HOME" 2>/dev/null || true

# Function to safely migrate a legacy directory to persistent destination
migrate_acme_directory() {
    src="$1"
    dst="$2"

    [ ! -d "$src" ] && return 0
    [ -L "$src" ] && return 0

    flist=$(mktemp 2>/dev/null || echo "/tmp/acme_flist.$$")
    if ! find "$src" -mindepth 1 -print0 > "$flist" 2>/dev/null; then
        echo "[MIKROZIA-ACME] ERROR: Traversal failed for source directory $src" >&2
        rm -f "$flist"
        return 1
    fi

    # If source has no files or subdirectories, preserve it and prepare for symlink
    if [ ! -s "$flist" ]; then
        rm -f "$flist"
        backup_empty="${src}.migrated"
        if [ -e "$backup_empty" ] || [ -L "$backup_empty" ]; then
            backup_empty="${src}.migrated.$(date +%Y%m%d%H%M%S)"
        fi
        mv "$src" "$backup_empty" 2>/dev/null || true
        return 0
    fi

    # 1. Conflict Detection: detect any conflicting files before making any modifications
    tmp_conflicts=$(mktemp 2>/dev/null || echo "/tmp/mikrozia_acme_conflicts.$$")
    conflict_count=0
    while IFS= read -r -d '' item; do
        rel="${item#$src/}"
        d="$dst/$rel"
        if [ -e "$d" ] || [ -L "$d" ]; then
            if [ -d "$item" ] && [ -d "$d" ]; then
                continue
            elif [ -f "$item" ] && [ -f "$d" ]; then
                if ! cmp -s "$item" "$d"; then
                    echo "$rel (content mismatch)" >> "$tmp_conflicts"
                    conflict_count=$((conflict_count + 1))
                fi
            elif [ -L "$item" ] && [ -L "$d" ]; then
                if [ "$(readlink "$item")" != "$(readlink "$d")" ]; then
                    echo "$rel (symlink target mismatch)" >> "$tmp_conflicts"
                    conflict_count=$((conflict_count + 1))
                fi
            else
                echo "$rel (entry type mismatch)" >> "$tmp_conflicts"
                conflict_count=$((conflict_count + 1))
            fi
        fi
    done < "$flist"

    if [ $conflict_count -ne 0 ] || [ -s "$tmp_conflicts" ]; then
        echo "[MIKROZIA-ACME] ERROR: Conflicting files detected between $src and $dst:" >&2
        cat "$tmp_conflicts" >&2
        rm -f "$tmp_conflicts" "$flist"
        echo "[MIKROZIA-ACME] Migration aborted safely. Source and destination remain untouched." >&2
        return 1
    fi
    rm -f "$tmp_conflicts"

    # 2. Copy Missing Items: preserve permissions, timestamps, mode, and hidden files
    copy_failed=0
    while IFS= read -r -d '' item; do
        rel="${item#$src/}"
        d="$dst/$rel"
        if [ -d "$item" ]; then
            if [ ! -d "$d" ]; then
                if ! mkdir -p "$d" 2>/dev/null; then
                    echo "[MIKROZIA-ACME] ERROR: Failed to create destination directory: $d" >&2
                    copy_failed=1
                    break
                fi
            fi
        elif [ -f "$item" ] || [ -L "$item" ]; then
            if [ ! -e "$d" ] && [ ! -L "$d" ]; then
                parent_d="$(dirname "$d")"
                if [ ! -d "$parent_d" ]; then
                    if ! mkdir -p "$parent_d" 2>/dev/null; then
                        echo "[MIKROZIA-ACME] ERROR: Failed to create parent directory for $d" >&2
                        copy_failed=1
                        break
                    fi
                fi
                if ! cp -a "$item" "$d" 2>/dev/null; then
                    echo "[MIKROZIA-ACME] ERROR: Failed to copy $item to $d" >&2
                    copy_failed=1
                    break
                fi
            fi
        fi
    done < "$flist"

    if [ $copy_failed -ne 0 ]; then
        echo "[MIKROZIA-ACME] ERROR: Copy operation failed for $src. Preserving source data untouched." >&2
        rm -f "$flist"
        return 1
    fi

    # 3. Verification: verify that every single item from source is preserved in destination
    verify_failed=0
    tmp_unverified=$(mktemp 2>/dev/null || echo "/tmp/mikrozia_acme_unverified.$$")
    while IFS= read -r -d '' item; do
        rel="${item#$src/}"
        d="$dst/$rel"
        if [ -d "$item" ]; then
            if [ ! -d "$d" ]; then
                echo "$rel (missing directory in destination)" >> "$tmp_unverified"
                verify_failed=1
            fi
        elif [ -f "$item" ]; then
            if [ ! -f "$d" ] || ! cmp -s "$item" "$d"; then
                echo "$rel (missing or corrupted file in destination)" >> "$tmp_unverified"
                verify_failed=1
            fi
        elif [ -L "$item" ]; then
            if [ ! -L "$d" ] || [ "$(readlink "$item")" != "$(readlink "$d")" ]; then
                echo "$rel (missing or mismatched symlink in destination)" >> "$tmp_unverified"
                verify_failed=1
            fi
        fi
    done < "$flist"
    rm -f "$flist"

    if [ $verify_failed -ne 0 ] || [ -s "$tmp_unverified" ]; then
        echo "[MIKROZIA-ACME] ERROR: Preservation verification failed for $src:" >&2
        cat "$tmp_unverified" >&2
        rm -f "$tmp_unverified"
        echo "[MIKROZIA-ACME] Source directory $src is preserved untouched." >&2
        return 1
    fi
    rm -f "$tmp_unverified"

    # 4. Source Preservation: rename the legacy source rather than deleting it
    backup_dir="${src}.migrated"
    if [ -e "$backup_dir" ] || [ -L "$backup_dir" ]; then
        backup_dir="${src}.migrated.$(date +%Y%m%d%H%M%S)"
    fi
    if ! mv "$src" "$backup_dir" 2>/dev/null; then
        echo "[MIKROZIA-ACME] ERROR: Failed to rename legacy directory $src to $backup_dir" >&2
        return 1
    fi
    echo "[MIKROZIA-ACME] Verified migration complete. Legacy source preserved in $backup_dir"
    return 0
}

# Helper to ensure symlink points to destination safely
ensure_acme_symlink() {
    link_path="$1"
    target_path="$2"

    if [ -L "$link_path" ]; then
        cur_target=$(readlink "$link_path" 2>/dev/null || true)
        res_cur=$(realpath "$link_path" 2>/dev/null || readlink -f "$link_path" 2>/dev/null || true)
        res_tgt=$(realpath "$target_path" 2>/dev/null || readlink -f "$target_path" 2>/dev/null || true)

        # 1. Already points to target_path (exact or canonical)
        if [ "$cur_target" = "$target_path" ] || { [ -n "$res_cur" ] && [ -n "$res_tgt" ] && [ "$res_cur" = "$res_tgt" ]; }; then
            return 0
        fi

        # 2. Broken symlink (target does not exist)
        if [ ! -e "$link_path" ]; then
            rm -f "$link_path"
            ln -s "$target_path" "$link_path"
            return 0
        fi

        # 3. Existing symlink points to a real directory
        if [ -n "$res_cur" ] && [ -d "$res_cur" ]; then
            # Inspect if target directory contains data
            if [ -n "$(find "$res_cur" -mindepth 1 2>/dev/null)" ]; then
                echo "[MIKROZIA-ACME] Populated legacy directory detected via symlink $link_path -> $res_cur. Migrating..."
                migrate_acme_directory "$res_cur" "$target_path" || return 1
            fi
            rm -f "$link_path"
            ln -s "$target_path" "$link_path"
            return 0
        elif [ -n "$res_cur" ] && [ -f "$res_cur" ]; then
            mv "$res_cur" "${res_cur}.file.bak" 2>/dev/null || true
            rm -f "$link_path"
            ln -s "$target_path" "$link_path"
            return 0
        else
            rm -f "$link_path"
            ln -s "$target_path" "$link_path"
            return 0
        fi
    elif [ -d "$link_path" ]; then
        migrate_acme_directory "$link_path" "$target_path" || return 1
    elif [ -e "$link_path" ]; then
        mv "$link_path" "${link_path}.file.bak" 2>/dev/null || true
    fi

    ln -s "$target_path" "$link_path"
}

# Main migration execution
if [ -d "$ACME_CERT_DIR" ]; then
    mkdir -p "$ACME_PERSIST_DIR"
    ensure_acme_symlink "$ACME_ROOT_DIR" "$ACME_PERSIST_DIR" || exit 1
    if [ "$ACME_SLASH_DIR" != "$ACME_ROOT_DIR" ]; then
        ensure_acme_symlink "$ACME_SLASH_DIR" "$ACME_PERSIST_DIR" || exit 1
    fi
else
    mkdir -p "$ACME_ROOT_DIR"
    if [ "$ACME_SLASH_DIR" != "$ACME_ROOT_DIR" ]; then
        ensure_acme_symlink "$ACME_SLASH_DIR" "$ACME_ROOT_DIR" || exit 1
    fi
fi

# Allow testing or running migration-only mode without starting services
if [ "$1" = "--migrate-acme-only" ] || [ "${ACME_MIGRATE_ONLY:-false}" = "true" ]; then
    exit 0
fi

# Ensure crontab directory exists in Alpine
mkdir -p /etc/crontabs /var/spool/cron/crontabs

# Ensure Alpine BusyBox crond daemon is running for periodic tasks & ACME renewal
if ! pgrep crond >/dev/null 2>&1; then
    crond -b -L /var/log/cron.log
    if pgrep crond >/dev/null 2>&1; then
        echo "[MIKROZIA-CRON] Background scheduler (crond) started successfully."
    else
        echo "[MIKROZIA-CRON] WARNING: Failed to start background scheduler (crond)!" >&2
    fi
else
    echo "[MIKROZIA-CRON] Background scheduler (crond) is already running."
fi

# Certificate auto-renewal: acme.sh relies on a root crontab entry and crond.
# In an ephemeral container rootfs, crontab entries must be re-registered on boot
# so renewals actually fire across container recreation.
acme_bin=""
if [ -f "$ACME_ROOT_DIR/acme.sh" ]; then
    acme_bin="$ACME_ROOT_DIR/acme.sh"
elif [ -f "$ACME_PERSIST_DIR/acme.sh" ]; then
    acme_bin="$ACME_PERSIST_DIR/acme.sh"
fi

if [ -n "$acme_bin" ]; then
    cron_output=$("$acme_bin" --install-cronjob 2>&1)
    cron_status=$?
    if [ $cron_status -eq 0 ]; then
        echo "[MIKROZIA-ACME] acme.sh renewal cron job registered."
    else
        echo "[MIKROZIA-ACME] WARNING: acme.sh --install-cronjob failed (exit code $cron_status): $cron_output" >&2
    fi

    if crontab -l 2>/dev/null | grep -q "acme.sh"; then
        echo "[MIKROZIA-ACME] Verified acme.sh renewal schedule in crontab."
    else
        echo "[MIKROZIA-ACME] WARNING: acme.sh renewal schedule not found in crontab!" >&2
    fi
fi

# Allow testing or running cron/renewal setup mode without starting services
if [ "$1" = "--cron-setup-only" ] || [ "${CRON_SETUP_ONLY:-false}" = "true" ]; then
    exit 0
fi

# Run x-ui or custom command
if [ $# -gt 0 ] && [ "$1" != "./x-ui" ]; then
    exec "$@"
else
    exec /app/x-ui
fi
